package main

import (
	"context"
	"database/sql"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

// Local e2e coverage for join links as they stand today:
// create (open/gated) via JSON, redeem via JSON, owner approve/reject via JSON,
// and the thin /join/{token} HTML page (Battle.net stands in as a consent cookie).

type joinFixture struct {
	t          *testing.T
	srv        *httptest.Server
	store      *Store
	owner      contract.Player
	ownerAPI   apiClient
	group      contract.GroupSummary
}

func newJoinFixture(t *testing.T) *joinFixture {
	t.Helper()
	resetDB(t)
	store := NewStore(testPool)
	srv := httptest.NewServer((&Server{
		store:         store,
		publicBaseURL: "https://api.example.test",
	}).Handler())
	t.Cleanup(srv.Close)
	owner, ownerToken := devSession(t, "bnet-owner", "Owner#1", "Owner")
	return &joinFixture{
		t: t, srv: srv, store: store, owner: owner,
		ownerAPI: apiClient{base: srv.URL, token: ownerToken, http: srv.Client()},
		group:    devGroup(t, owner.ID, "tutorial", "Tutorial"),
	}
}

func (f *joinFixture) guestWithChars(account, tag, name string, chars ...contract.CharacterUpsert) (contract.Player, apiClient, []contract.Character) {
	f.t.Helper()
	player, token := devSession(f.t, account, tag, name)
	var out []contract.Character
	for _, in := range chars {
		in.BattleTag = tag
		c, err := f.store.UpsertCharacter(context.Background(), player.ID, in)
		if err != nil {
			f.t.Fatal(err)
		}
		out = append(out, c)
	}
	return player, apiClient{base: f.srv.URL, token: token, http: f.srv.Client()}, out
}

func (f *joinFixture) createLink(mode string, expiresInSec int, maxUses *int) contract.JoinLink {
	f.t.Helper()
	body := map[string]any{"mode": mode, "expires_in_sec": expiresInSec}
	if maxUses != nil {
		body["max_uses"] = *maxUses
	}
	var link contract.JoinLink
	resp, err := f.ownerAPI.do(http.MethodPost, "/v1/groups/"+f.group.ID+"/join-links", body, &link)
	if err != nil || resp.StatusCode != http.StatusCreated || link.Token == "" || link.URL == "" {
		f.t.Fatalf("create %s link %d %v %+v", mode, statusOf(resp), err, link)
	}
	if link.Mode != mode {
		f.t.Fatalf("mode want %s got %s", mode, link.Mode)
	}
	if !strings.HasPrefix(link.Token, "gsj_") || !strings.Contains(link.URL, "/join/"+link.Token) {
		f.t.Fatalf("token/url %+v", link)
	}
	return link
}

func participatingInGroup(t *testing.T, characterID, groupID string) (inGroup, participating bool) {
	t.Helper()
	var role sql.NullString
	err := testPool.QueryRow(context.Background(), `
		SELECT c.participating, gm.role
		FROM characters c
		LEFT JOIN group_members gm ON gm.player_id = c.player_id AND gm.group_id=$2
		WHERE c.id=$1`, characterID, groupID).Scan(&participating, &role)
	if err != nil {
		t.Fatal(err)
	}
	return role.Valid, participating
}

func TestE2ECreateJoinLinkOpenOrGated(t *testing.T) {
	f := newJoinFixture(t)

	maxUses := 3
	open := f.createLink("open", 3600, &maxUses)
	if open.MaxUses == nil || *open.MaxUses != 3 {
		t.Fatalf("max_uses %+v", open)
	}

	gated := f.createLink("gated", 86400, nil)
	if gated.MaxUses != nil {
		t.Fatalf("gated should omit max_uses %+v", gated)
	}

	// Default mode is gated when omitted.
	var def contract.JoinLink
	resp, err := f.ownerAPI.do(http.MethodPost, "/v1/groups/"+f.group.ID+"/join-links", map[string]any{
		"expires_in_sec": 7200,
	}, &def)
	if err != nil || resp.StatusCode != http.StatusCreated || def.Mode != "gated" {
		t.Fatalf("default mode %d %v %+v", statusOf(resp), err, def)
	}

	_, guestAPI, _ := f.guestWithChars("bnet-guest", "Guest#9", "Guest")
	resp, err = guestAPI.do(http.MethodPost, "/v1/groups/"+f.group.ID+"/join-links", map[string]any{"mode": "open"}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-owner create %d %v", statusOf(resp), err)
	}

	var listed struct {
		JoinLinks []contract.JoinLink `json:"join_links"`
	}
	resp, err = f.ownerAPI.do(http.MethodGet, "/v1/groups/"+f.group.ID+"/join-links", nil, &listed)
	if err != nil || resp.StatusCode != http.StatusOK || len(listed.JoinLinks) != 3 {
		t.Fatalf("list %d %v %+v", statusOf(resp), err, listed.JoinLinks)
	}
	for _, l := range listed.JoinLinks {
		if l.Token != "" || l.URL != "" {
			t.Fatalf("list must not echo secrets %+v", l)
		}
	}
}

func TestE2EOpenJoinAddsSelectedCharacter(t *testing.T) {
	f := newJoinFixture(t)
	_, guestAPI, chars := f.guestWithChars("bnet-guest", "Guest#9", "Guest",
		contract.CharacterUpsert{Name: "Magni", Realm: "Everlook", Level: 10, Race: "Dwarf", Class: "Warrior"},
		contract.CharacterUpsert{Name: "Mira", Realm: "Everlook", Level: 8, Race: "Human", Class: "Mage"},
	)
	magni, mira := chars[0], chars[1]
	link := f.createLink("open", 3600, nil)

	var me struct {
		Characters []contract.Character `json:"characters"`
	}
	resp, err := guestAPI.do(http.MethodGet, "/v1/me/characters", nil, &me)
	if err != nil || resp.StatusCode != http.StatusOK || len(me.Characters) != 2 {
		t.Fatalf("roster %d %v %+v", statusOf(resp), err, me.Characters)
	}

	var info contract.JoinLinkInfo
	resp, err = guestAPI.do(http.MethodGet, "/v1/join/"+link.Token, nil, &info)
	if err != nil || resp.StatusCode != http.StatusOK || info.Mode != "open" || len(info.Characters) != 2 {
		t.Fatalf("info %d %v %+v", statusOf(resp), err, info)
	}

	var redeemed contract.JoinRequest
	resp, err = guestAPI.do(http.MethodPost, "/v1/join/"+link.Token, map[string]any{
		"character_ids": []string{magni.ID},
	}, &redeemed)
	if err != nil || resp.StatusCode != http.StatusOK || redeemed.Status != "approved" {
		t.Fatalf("redeem %d %v %+v", statusOf(resp), err, redeemed)
	}

	inGroup, part := participatingInGroup(t, magni.ID, f.group.ID)
	if !inGroup || !part {
		t.Fatalf("selected char inGroup=%v participating=%v", inGroup, part)
	}
	inGroup, part = participatingInGroup(t, mira.ID, f.group.ID)
	if !inGroup || part {
		t.Fatalf("unchecked alt must stay non-participating inGroup=%v participating=%v", inGroup, part)
	}
}

func TestE2EGatedApproveAddsCharacter(t *testing.T) {
	f := newJoinFixture(t)
	guest, guestAPI, chars := f.guestWithChars("bnet-guest", "Guest#9", "Guest",
		contract.CharacterUpsert{Name: "Magni", Realm: "Everlook", Level: 10, Race: "Dwarf", Class: "Warrior"},
	)
	link := f.createLink("gated", 3600, nil)

	var pending contract.JoinRequest
	resp, err := guestAPI.do(http.MethodPost, "/v1/join/"+link.Token, map[string]any{
		"character_ids": []string{chars[0].ID},
	}, &pending)
	if err != nil || resp.StatusCode != http.StatusOK || pending.Status != "pending" || pending.ID == "" {
		t.Fatalf("request %d %v %+v", statusOf(resp), err, pending)
	}
	inGroup, _ := participatingInGroup(t, chars[0].ID, f.group.ID)
	if inGroup {
		t.Fatal("gated redeem must not join before approve")
	}

	var listed struct {
		JoinRequests []contract.JoinRequest `json:"join_requests"`
	}
	resp, err = f.ownerAPI.do(http.MethodGet, "/v1/groups/"+f.group.ID+"/join-requests?status=pending", nil, &listed)
	if err != nil || resp.StatusCode != http.StatusOK || len(listed.JoinRequests) != 1 {
		t.Fatalf("list pending %d %v %+v", statusOf(resp), err, listed.JoinRequests)
	}
	req := listed.JoinRequests[0]
	if req.ID != pending.ID || req.PlayerID != guest.ID || len(req.CharacterIDs) != 1 || req.CharacterIDs[0] != chars[0].ID {
		t.Fatalf("pending row %+v", req)
	}

	var approved contract.JoinRequest
	resp, err = f.ownerAPI.do(http.MethodPost, "/v1/groups/"+f.group.ID+"/join-requests/"+pending.ID+"/approve", map[string]any{}, &approved)
	if err != nil || resp.StatusCode != http.StatusOK || approved.Status != "approved" {
		t.Fatalf("approve %d %v %+v", statusOf(resp), err, approved)
	}
	inGroup, part := participatingInGroup(t, chars[0].ID, f.group.ID)
	if !inGroup || !part {
		t.Fatalf("after approve inGroup=%v participating=%v", inGroup, part)
	}
}

func TestE2EGatedRejectLeavesOut(t *testing.T) {
	f := newJoinFixture(t)
	_, guestAPI, chars := f.guestWithChars("bnet-out", "Out#1", "Out",
		contract.CharacterUpsert{Name: "Thrall", Realm: "Everlook", Level: 12, Race: "Orc", Class: "Shaman"},
	)
	link := f.createLink("gated", 3600, nil)

	var pending contract.JoinRequest
	resp, err := guestAPI.do(http.MethodPost, "/v1/join/"+link.Token, map[string]any{
		"character_ids": []string{chars[0].ID},
	}, &pending)
	if err != nil || resp.StatusCode != http.StatusOK || pending.Status != "pending" {
		t.Fatalf("request %d %v %+v", statusOf(resp), err, pending)
	}

	var rejected contract.JoinRequest
	resp, err = f.ownerAPI.do(http.MethodPost, "/v1/groups/"+f.group.ID+"/join-requests/"+pending.ID+"/reject", map[string]any{}, &rejected)
	if err != nil || resp.StatusCode != http.StatusOK || rejected.Status != "rejected" {
		t.Fatalf("reject %d %v %+v", statusOf(resp), err, rejected)
	}
	inGroup, part := participatingInGroup(t, chars[0].ID, f.group.ID)
	if inGroup || part {
		t.Fatalf("rejected must stay out inGroup=%v participating=%v", inGroup, part)
	}

	resp, err = f.ownerAPI.do(http.MethodPost, "/v1/groups/"+f.group.ID+"/join-requests/"+pending.ID+"/approve", map[string]any{}, nil)
	if err != nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("re-approve rejected %d %v", statusOf(resp), err)
	}
}

func TestE2EClickJoinLinkHTMLOpen(t *testing.T) {
	f := newJoinFixture(t)
	guest, _, chars := f.guestWithChars("bnet-guest", "Guest#9", "Guest",
		contract.CharacterUpsert{Name: "Magni", Realm: "Everlook", Level: 10, Race: "Dwarf", Class: "Warrior"},
		contract.CharacterUpsert{Name: "Mira", Realm: "Everlook", Level: 8, Race: "Human", Class: "Mage"},
	)
	link := f.createLink("open", 3600, nil)

	// Anonymous click: sign-in CTA (Battle.net button). No roster yet.
	anon, err := http.Get(f.srv.URL + "/join/" + link.Token)
	if err != nil {
		t.Fatal(err)
	}
	anonBody, _ := io.ReadAll(anon.Body)
	anon.Body.Close()
	if anon.StatusCode != http.StatusOK {
		t.Fatalf("anon html %d", anon.StatusCode)
	}
	page := string(anonBody)
	if !strings.Contains(page, "Sign in") || !strings.Contains(page, "/join/"+link.Token+"/login") {
		t.Fatalf("expected sign-in CTA, got:\n%s", page)
	}
	if !strings.Contains(page, "join immediately") {
		t.Fatalf("expected open-mode copy:\n%s", page)
	}

	// After Battle.net (simulated with join consent cookie): character checkboxes.
	consent, err := f.store.IssueConsentSession(context.Background(), guest.ID)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	u, err := url.Parse(f.srv.URL + "/join/" + link.Token)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: joinCookieName, Value: consent, Path: "/join"}})

	signed, err := client.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	signedBody, _ := io.ReadAll(signed.Body)
	signed.Body.Close()
	if signed.StatusCode != http.StatusOK {
		t.Fatalf("signed html %d", signed.StatusCode)
	}
	page = string(signedBody)
	if !strings.Contains(page, "Magni") || !strings.Contains(page, "Mira") || !strings.Contains(page, `name="character_id"`) {
		t.Fatalf("expected character checkboxes:\n%s", page)
	}
	if !strings.Contains(page, "Guest#9") {
		t.Fatalf("expected signed-in BattleTag:\n%s", page)
	}

	form := url.Values{}
	form.Add("character_id", chars[0].ID)
	posted, err := client.PostForm(u.String(), form)
	if err != nil {
		t.Fatal(err)
	}
	postBody, _ := io.ReadAll(posted.Body)
	posted.Body.Close()
	if posted.StatusCode != http.StatusOK {
		t.Fatalf("post html %d", posted.StatusCode)
	}
	page = string(postBody)
	if !strings.Contains(page, "You are in") {
		t.Fatalf("expected success copy:\n%s", page)
	}

	inGroup, part := participatingInGroup(t, chars[0].ID, f.group.ID)
	if !inGroup || !part {
		t.Fatalf("html open join inGroup=%v participating=%v", inGroup, part)
	}
	inGroup, part = participatingInGroup(t, chars[1].ID, f.group.ID)
	if !inGroup || part {
		t.Fatalf("unchecked alt via html inGroup=%v participating=%v", inGroup, part)
	}
}

func TestE2EClickJoinLinkHTMLGated(t *testing.T) {
	f := newJoinFixture(t)
	guest, _, chars := f.guestWithChars("bnet-guest", "Guest#9", "Guest",
		contract.CharacterUpsert{Name: "Magni", Realm: "Everlook", Level: 10, Race: "Dwarf", Class: "Warrior"},
	)
	link := f.createLink("gated", 3600, nil)

	consent, err := f.store.IssueConsentSession(context.Background(), guest.ID)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	u, err := url.Parse(f.srv.URL + "/join/" + link.Token)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: joinCookieName, Value: consent, Path: "/join"}})

	getResp, err := client.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	getBody, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()
	page := string(getBody)
	if !strings.Contains(page, "Request join") || !strings.Contains(page, "owner will confirm") {
		t.Fatalf("expected gated copy:\n%s", page)
	}

	form := url.Values{}
	form.Add("character_id", chars[0].ID)
	posted, err := client.PostForm(u.String(), form)
	if err != nil {
		t.Fatal(err)
	}
	postBody, _ := io.ReadAll(posted.Body)
	posted.Body.Close()
	page = string(postBody)
	if !strings.Contains(page, "Request sent") {
		t.Fatalf("expected pending copy:\n%s", page)
	}
	inGroup, _ := participatingInGroup(t, chars[0].ID, f.group.ID)
	if inGroup {
		t.Fatal("html gated must not join before approve")
	}

	var listed struct {
		JoinRequests []contract.JoinRequest `json:"join_requests"`
	}
	resp, err := f.ownerAPI.do(http.MethodGet, "/v1/groups/"+f.group.ID+"/join-requests", nil, &listed)
	if err != nil || resp.StatusCode != http.StatusOK || len(listed.JoinRequests) != 1 {
		t.Fatalf("owner sees request %d %v %+v", statusOf(resp), err, listed.JoinRequests)
	}
}

func TestE2EClickJoinLinkHTMLEmptyRoster(t *testing.T) {
	f := newJoinFixture(t)
	guest, _, _ := f.guestWithChars("bnet-new", "New#1", "New")
	link := f.createLink("open", 3600, nil)

	consent, err := f.store.IssueConsentSession(context.Background(), guest.ID)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	u, err := url.Parse(f.srv.URL + "/join/" + link.Token)
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: joinCookieName, Value: consent, Path: "/join"}})

	resp, err := client.Get(u.String())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	if !strings.Contains(page, "No characters yet") || !strings.Contains(page, "companion") {
		t.Fatalf("expected empty-roster guidance:\n%s", page)
	}
}

func TestE2EJoinLinkPersonalForbidden(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool)}).Handler())
	t.Cleanup(srv.Close)
	_, token := devSession(t, "bnet-p", "Pers#1", "Pers")
	api := apiClient{base: srv.URL, token: token, http: srv.Client()}
	var sync contract.SyncResponse
	resp, err := api.do(http.MethodGet, "/v1/sync", nil, &sync)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sync %d %v", statusOf(resp), err)
	}
	personalID := ""
	for _, g := range sync.Groups {
		if g.Personal {
			personalID = g.ID
			break
		}
	}
	if personalID == "" {
		t.Fatal("expected personal group")
	}
	resp, err = api.do(http.MethodPost, "/v1/groups/"+personalID+"/join-links", map[string]any{"mode": "open"}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("personal join link %d %v", statusOf(resp), err)
	}
}
