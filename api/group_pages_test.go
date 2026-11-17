package main

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func groupsBrowser(t *testing.T, base, playerID string) *http.Client {
	t.Helper()
	raw, err := NewStore(testPool).IssueConsentSession(context.Background(), playerID)
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	u, err := url.Parse(base + "/groups")
	if err != nil {
		t.Fatal(err)
	}
	jar.SetCookies(u, []*http.Cookie{{Name: groupsCookieName, Value: raw, Path: "/groups"}})
	return client
}

func readResp(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func postGroupsForm(t *testing.T, client *http.Client, rawurl string, form url.Values) (int, string) {
	t.Helper()
	resp, err := client.PostForm(rawurl, form)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, readResp(t, resp)
}

func TestGroupConsoleOwnerFlow(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool), devToken: "dev-secret", publicBaseURL: "http://groups.example"}).Handler())
	t.Cleanup(srv.Close)
	owner, ownerToken := devSession(t, "bnet-owner", "TestOwner#1001", "TestOwner")
	guest, _ := devSession(t, "bnet-guest", "TestGuest#2002", "TestGuest")
	other, _ := devSession(t, "bnet-other", "OtherOwner#3003", "OtherOwner")
	group := devGroup(t, owner.ID, "northshire", "Northshire")
	foreign := devGroup(t, other.ID, "redridge", "Redridge")
	joinGroup(t, foreign.ID, owner.ID)

	anon, err := http.Get(srv.URL + "/groups")
	if err != nil {
		t.Fatal(err)
	}
	anonPage := readResp(t, anon)
	if anon.StatusCode != http.StatusOK || !strings.Contains(anonPage, "Sign in with Battle.net") || strings.Contains(anonPage, "Northshire") {
		t.Fatalf("anon page %d leaked or missing sign-in:\n%s", anon.StatusCode, anonPage)
	}
	if strings.Contains(anonPage, "<script") {
		t.Fatal("console page must not ship scripts")
	}

	ownerWeb := groupsBrowser(t, srv.URL, owner.ID)
	home, err := ownerWeb.Get(srv.URL + "/groups")
	if err != nil {
		t.Fatal(err)
	}
	homePage := readResp(t, home)
	if home.StatusCode != http.StatusOK || !strings.Contains(homePage, "Northshire") || !strings.Contains(homePage, "Create a group") {
		t.Fatalf("index %d\n%s", home.StatusCode, homePage)
	}
	if !strings.Contains(homePage, "TestOwner#1001") || !strings.Contains(homePage, "/groups/"+group.ID) {
		t.Fatalf("index missing owner manage link:\n%s", homePage)
	}
	if !strings.Contains(homePage, "Redridge") || !strings.Contains(homePage, "Owner only") {
		t.Fatalf("member group should be listed without a manage action:\n%s", homePage)
	}
	if strings.Contains(homePage, "/groups/"+foreign.ID) {
		t.Fatalf("member must not get a manage link:\n%s", homePage)
	}

	console, err := ownerWeb.Get(srv.URL + "/groups/" + group.ID)
	if err != nil {
		t.Fatal(err)
	}
	consolePage := readResp(t, console)
	if console.StatusCode != http.StatusOK || !strings.Contains(consolePage, "Send invite") || !strings.Contains(consolePage, "Create join link") {
		t.Fatalf("console %d\n%s", console.StatusCode, consolePage)
	}
	if !strings.Contains(consolePage, "does not create a character") || strings.Contains(consolePage, "Create character") {
		t.Fatalf("console must not offer character editing:\n%s", consolePage)
	}
	if console.Header.Get("Content-Security-Policy") == "" || console.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("headers csp=%q cache=%q", console.Header.Get("Content-Security-Policy"), console.Header.Get("Cache-Control"))
	}

	code, page := postGroupsForm(t, ownerWeb, srv.URL+"/groups/"+group.ID, url.Values{
		"action": {"save"}, "name": {"Northshire Watch"}, "locale": {"nb"},
	})
	if code != http.StatusOK || !strings.Contains(page, "Group updated.") || !strings.Contains(page, "Northshire Watch") {
		t.Fatalf("save %d\n%s", code, page)
	}
	if !strings.Contains(page, `value="nb" selected`) && !strings.Contains(page, `selected>Norsk`) && !strings.Contains(page, `value="nb" selected`) {
		if !strings.Contains(page, "nb") || !strings.Contains(page, "selected") {
			t.Fatalf("locale not selected:\n%s", page)
		}
	}
	var savedName, savedLocale, savedSlug string
	if err := testPool.QueryRow(context.Background(), `SELECT name, locale, slug FROM groups WHERE id=$1`, group.ID).Scan(&savedName, &savedLocale, &savedSlug); err != nil {
		t.Fatal(err)
	}
	if savedName != "Northshire Watch" || savedLocale != "nb" || savedSlug != "northshire" {
		t.Fatalf("saved name=%s locale=%s slug=%s", savedName, savedLocale, savedSlug)
	}

	code, page = postGroupsForm(t, ownerWeb, srv.URL+"/groups/"+group.ID, url.Values{
		"action": {"invite"}, "name": {"Theron"}, "realm": {"Everlook"}, "battletag": {"TestGuest#2002"},
	})
	if code != http.StatusOK || !strings.Contains(page, "Invite sent.") || !strings.Contains(page, "Theron") || !strings.Contains(page, "Everlook") {
		t.Fatalf("invite %d\n%s", code, page)
	}
	var inviteID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text FROM group_invites WHERE group_id=$1 AND character_name='Theron' AND status='pending'`, group.ID).Scan(&inviteID); err != nil {
		t.Fatal(err)
	}
	code, page = postGroupsForm(t, ownerWeb, srv.URL+"/groups/"+group.ID, url.Values{
		"action": {"revoke-invite"}, "invite_id": {inviteID},
	})
	if code != http.StatusOK || !strings.Contains(page, "Invite revoked.") || strings.Contains(page, ">Theron<") {
		t.Fatalf("revoke invite %d\n%s", code, page)
	}

	code, page = postGroupsForm(t, ownerWeb, srv.URL+"/groups/"+group.ID, url.Values{
		"action": {"mint-link"}, "mode": {"gated"}, "expires_in_sec": {"3600"},
	})
	if code != http.StatusOK || !strings.Contains(page, "Copy this join link") || !strings.Contains(page, "http://groups.example/join/gsj_") {
		t.Fatalf("mint %d\n%s", code, page)
	}
	var linkID, mode string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text, mode FROM group_join_links WHERE group_id=$1 AND revoked_at IS NULL`, group.ID).Scan(&linkID, &mode); err != nil {
		t.Fatal(err)
	}
	if mode != "gated" {
		t.Fatalf("mode %s", mode)
	}
	code, page = postGroupsForm(t, ownerWeb, srv.URL+"/groups/"+group.ID, url.Values{
		"action": {"revoke-link"}, "link_id": {linkID},
	})
	if code != http.StatusOK || !strings.Contains(page, "Join link revoked.") || !strings.Contains(page, "Revoked") {
		t.Fatalf("revoke link %d\n%s", code, page)
	}
	var revoked string
	if err := testPool.QueryRow(context.Background(), `SELECT COALESCE(revoked_at::text, '') FROM group_join_links WHERE id=$1`, linkID).Scan(&revoked); err != nil || revoked == "" {
		t.Fatalf("revoked_at %q err %v", revoked, err)
	}

	code, page = postGroupsForm(t, ownerWeb, srv.URL+"/groups/"+group.ID, url.Values{
		"action": {"mint-link"}, "mode": {"gated"}, "expires_in_sec": {"86400"}, "max_uses": {"4"},
	})
	if code != http.StatusOK || !strings.Contains(page, "/join/gsj_") {
		t.Fatalf("second mint %d\n%s", code, page)
	}
	char, err := NewStore(testPool).UpsertCharacter(context.Background(), guest.ID, contract.CharacterUpsert{
		Name: "Theron", Realm: "Everlook", Level: 12, Race: "Human", Class: "Warrior", BattleTag: "TestGuest#2002",
	})
	if err != nil {
		t.Fatal(err)
	}
	// The raw token is only on the page. Pull it back out of the HTML.
	const marker = "http://groups.example/join/"
	i := strings.Index(page, marker)
	if i < 0 {
		t.Fatalf("missing minted url:\n%s", page)
	}
	rawToken := page[i+len(marker):]
	if cut := strings.IndexAny(rawToken, " <&\n"); cut >= 0 {
		rawToken = rawToken[:cut]
	}
	pending, err := NewStore(testPool).RedeemJoinLink(context.Background(), guest.ID, rawToken, []string{char.ID})
	if err != nil || pending.Status != "pending" {
		t.Fatalf("redeem %v %+v", err, pending)
	}
	reviewed, err := ownerWeb.Get(srv.URL + "/groups/" + group.ID)
	if err != nil {
		t.Fatal(err)
	}
	reviewPage := readResp(t, reviewed)
	if !strings.Contains(reviewPage, "TestGuest#2002") || !strings.Contains(reviewPage, "Theron") || !strings.Contains(reviewPage, "Warrior") {
		t.Fatalf("request not listed:\n%s", reviewPage)
	}
	code, page = postGroupsForm(t, ownerWeb, srv.URL+"/groups/"+group.ID, url.Values{
		"action": {"reject"}, "request_id": {pending.ID},
	})
	if code != http.StatusOK || !strings.Contains(page, "Request rejected.") {
		t.Fatalf("reject %d\n%s", code, page)
	}
	var members int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM group_members WHERE group_id=$1 AND player_id=$2`, group.ID, guest.ID).Scan(&members); err != nil || members != 0 {
		t.Fatalf("rejected member count %d err %v", members, err)
	}

	code, page = postGroupsForm(t, ownerWeb, srv.URL+"/groups/"+group.ID, url.Values{
		"action": {"mint-link"}, "mode": {"gated"}, "expires_in_sec": {"3600"},
	})
	if code != http.StatusOK {
		t.Fatalf("third mint %d\n%s", code, page)
	}
	i = strings.Index(page, marker)
	if i < 0 {
		t.Fatalf("missing third url:\n%s", page)
	}
	rawToken = page[i+len(marker):]
	if cut := strings.IndexAny(rawToken, " <&\n"); cut >= 0 {
		rawToken = rawToken[:cut]
	}
	pending, err = NewStore(testPool).RedeemJoinLink(context.Background(), guest.ID, rawToken, []string{char.ID})
	if err != nil || pending.Status != "pending" {
		t.Fatalf("redeem again %v %+v", err, pending)
	}
	code, page = postGroupsForm(t, ownerWeb, srv.URL+"/groups/"+group.ID, url.Values{
		"action": {"approve"}, "request_id": {pending.ID},
	})
	if code != http.StatusOK || !strings.Contains(page, "Request approved.") || !strings.Contains(page, "TestGuest#2002") {
		t.Fatalf("approve %d\n%s", code, page)
	}
	if !strings.Contains(page, "Remove") {
		t.Fatalf("expected remove control:\n%s", page)
	}
	var participating bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT c.participating FROM characters c
		JOIN group_members gm ON gm.player_id=c.player_id AND gm.group_id=$2
		WHERE c.id=$1`, char.ID, group.ID).Scan(&participating); err != nil || !participating {
		t.Fatalf("approved participating=%v err=%v", participating, err)
	}
	code, page = postGroupsForm(t, ownerWeb, srv.URL+"/groups/"+group.ID, url.Values{
		"action": {"kick"}, "player_id": {guest.ID},
	})
	if code != http.StatusOK || !strings.Contains(page, "Member removed.") || strings.Contains(page, "TestGuest#2002") {
		t.Fatalf("kick %d\n%s", code, page)
	}
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM group_members WHERE group_id=$1 AND player_id=$2`, group.ID, guest.ID).Scan(&members); err != nil || members != 0 {
		t.Fatalf("kicked count %d err %v", members, err)
	}

	code, page = postGroupsForm(t, ownerWeb, srv.URL+"/groups", url.Values{
		"name": {"Duskwood"}, "slug": {"duskwood"}, "locale": {"en"},
	})
	if code != http.StatusOK || !strings.Contains(page, "Group created.") || !strings.Contains(page, "Duskwood") {
		t.Fatalf("create %d\n%s", code, page)
	}
	code, page = postGroupsForm(t, ownerWeb, srv.URL+"/groups", url.Values{
		"name": {"Duskwood again"}, "slug": {"duskwood"}, "locale": {"en"},
	})
	if code != http.StatusOK || !strings.Contains(page, "slug already exists") {
		t.Fatalf("duplicate slug %d\n%s", code, page)
	}

	guestWeb := groupsBrowser(t, srv.URL, guest.ID)
	denied, err := guestWeb.Get(srv.URL + "/groups/" + group.ID)
	if err != nil {
		t.Fatal(err)
	}
	deniedPage := readResp(t, denied)
	if denied.StatusCode != http.StatusForbidden || !strings.Contains(deniedPage, "You cannot manage this group.") || strings.Contains(deniedPage, "Send invite") {
		t.Fatalf("guest console %d\n%s", denied.StatusCode, deniedPage)
	}
	code, page = postGroupsForm(t, guestWeb, srv.URL+"/groups/"+group.ID, url.Values{
		"action": {"kick"}, "player_id": {owner.ID},
	})
	if code != http.StatusForbidden || strings.Contains(page, "Member removed.") {
		t.Fatalf("guest kick %d\n%s", code, page)
	}

	otherWeb := groupsBrowser(t, srv.URL, other.ID)
	foreignGet, err := otherWeb.Get(srv.URL + "/groups/" + group.ID)
	if err != nil {
		t.Fatal(err)
	}
	foreignPage := readResp(t, foreignGet)
	if foreignGet.StatusCode != http.StatusForbidden || strings.Contains(foreignPage, "Northshire") {
		t.Fatalf("other owner %d\n%s", foreignGet.StatusCode, foreignPage)
	}

	ownerAPI := apiClient{base: srv.URL, token: ownerToken, http: srv.Client()}
	var patched contract.GroupSummary
	resp, err := ownerAPI.do(http.MethodPatch, "/v1/groups/"+group.ID, map[string]string{
		"name": "Northshire Watch", "locale": "en",
	}, &patched)
	if err != nil || resp.StatusCode != http.StatusOK || patched.Name != "Northshire Watch" || patched.Locale != "en" || patched.Slug != "northshire" {
		t.Fatalf("patch %d %v %+v", statusOf(resp), err, patched)
	}
}

func TestPatchGroupForbiddenToNonOwners(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool), adminToken: "admin-test"}).Handler())
	t.Cleanup(srv.Close)
	owner, ownerToken := devSession(t, "bnet-owner", "TestOwner#1001", "TestOwner")
	member, memberToken := devSession(t, "bnet-member", "TestMember#4004", "TestMember")
	_, strangerToken := devSession(t, "bnet-stranger", "Stranger#5005", "Stranger")
	group := devGroup(t, owner.ID, "northshire", "Northshire")
	joinGroup(t, group.ID, member.ID)

	memberAPI := apiClient{base: srv.URL, token: memberToken, http: srv.Client()}
	resp, err := memberAPI.do(http.MethodPatch, "/v1/groups/"+group.ID, map[string]string{"name": "Nope", "locale": "en"}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("member patch %d %v", statusOf(resp), err)
	}
	strangerAPI := apiClient{base: srv.URL, token: strangerToken, http: srv.Client()}
	resp, err = strangerAPI.do(http.MethodPatch, "/v1/groups/"+group.ID, map[string]string{"name": "Nope", "locale": "en"}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("stranger patch %d %v", statusOf(resp), err)
	}
	adminAPI := apiClient{base: srv.URL, token: "admin-test", http: srv.Client()}
	resp, err = adminAPI.do(http.MethodPatch, "/v1/groups/"+group.ID, map[string]string{"name": "Nope", "locale": "en"}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin patch %d %v", statusOf(resp), err)
	}
	ownerAPI := apiClient{base: srv.URL, token: ownerToken, http: srv.Client()}
	resp, err = ownerAPI.do(http.MethodPatch, "/v1/groups/"+group.ID, map[string]string{"name": "", "locale": "en"}, nil)
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("empty name %d %v", statusOf(resp), err)
	}
	var name string
	if err := testPool.QueryRow(context.Background(), `SELECT name FROM groups WHERE id=$1`, group.ID).Scan(&name); err != nil || name != "Northshire" {
		t.Fatalf("name changed to %s (%v)", name, err)
	}
}

func TestGroupConsolePersonalHidesInvites(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool)}).Handler())
	t.Cleanup(srv.Close)
	owner, _ := devSession(t, "bnet-owner", "TestOwner#1001", "TestOwner")
	if err := NewStore(testPool).EnsurePersonalGroup(context.Background(), owner.ID); err != nil {
		t.Fatal(err)
	}
	var personalID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text FROM groups WHERE personal AND owner_player_id=$1`, owner.ID).Scan(&personalID); err != nil {
		t.Fatal(err)
	}
	web := groupsBrowser(t, srv.URL, owner.ID)
	resp, err := web.Get(srv.URL + "/groups/" + personalID)
	if err != nil {
		t.Fatal(err)
	}
	page := readResp(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(page, "personal group") {
		t.Fatalf("personal %d\n%s", resp.StatusCode, page)
	}
	if strings.Contains(page, "Create join link") || strings.Contains(page, "Send invite") {
		t.Fatalf("personal group exposed invite tools:\n%s", page)
	}
	code, page := postGroupsForm(t, web, srv.URL+"/groups/"+personalID, url.Values{
		"action": {"mint-link"}, "mode": {"open"}, "expires_in_sec": {"3600"},
	})
	if code != http.StatusOK || !strings.Contains(page, "Personal groups cannot") {
		t.Fatalf("personal mint %d\n%s", code, page)
	}
}

func TestGroupsLoginAndDevLogin(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool), devToken: "dev-secret"}).Handler())
	t.Cleanup(srv.Close)
	owner, _ := devSession(t, "bnet-owner", "TestOwner#1001", "TestOwner")
	group := devGroup(t, owner.ID, "northshire", "Northshire")

	noFollow := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	resp, err := noFollow.Get(srv.URL + "/groups/dev-login")
	if err != nil {
		t.Fatal(err)
	}
	body := readResp(t, resp)
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(body, "unauthorized") {
		t.Fatalf("dev-login without token %d %s", resp.StatusCode, body)
	}

	resp, err = http.Get(srv.URL + "/groups/login")
	if err != nil {
		t.Fatal(err)
	}
	body = readResp(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Battle.net login is not configured") {
		t.Fatalf("login unconfigured %d\n%s", resp.StatusCode, body)
	}
	resp, err = http.Get(srv.URL + "/groups/login?next=https://evil.example")
	if err != nil {
		t.Fatal(err)
	}
	body = readResp(t, resp)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(body, "not a group") {
		t.Fatalf("bad next %d\n%s", resp.StatusCode, body)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	resp, err = client.Get(srv.URL + "/groups/dev-login?token=dev-secret&battletag=" + url.QueryEscape("TestOwner#1001") + "&next=" + group.ID)
	if err != nil {
		t.Fatal(err)
	}
	body = readResp(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Northshire") || !strings.Contains(body, "Send invite") || !strings.Contains(body, "TestOwner#1001") {
		t.Fatalf("dev login %d\n%s", resp.StatusCode, body)
	}

	resp, err = client.Get(srv.URL + "/groups?signout=1")
	if err != nil {
		t.Fatal(err)
	}
	body = readResp(t, resp)
	if resp.StatusCode != http.StatusOK || !strings.Contains(body, "Sign in with Battle.net") || strings.Contains(body, "Send invite") {
		t.Fatalf("sign out %d\n%s", resp.StatusCode, body)
	}
}
