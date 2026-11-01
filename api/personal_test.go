package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestEnsurePersonalGroupOnSync(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool)}).Handler())
	t.Cleanup(srv.Close)
	player, token := devSession(t, "bnet-new", "Newbie#1", "Newbie")
	store := NewStore(testPool)
	if _, err := store.UpsertCharacter(context.Background(), player.ID, contract.CharacterUpsert{
		Name: "Magni", Realm: "Everlook", Level: 5, BattleTag: "Newbie#1",
	}); err != nil {
		t.Fatal(err)
	}
	api := apiClient{base: srv.URL, token: token, http: srv.Client()}

	var sync contract.SyncResponse
	resp, err := api.do(http.MethodGet, "/v1/sync", nil, &sync)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sync %d %v", statusOf(resp), err)
	}
	if len(sync.Groups) != 1 || !sync.Groups[0].Personal || sync.Groups[0].Name != "Personal" {
		t.Fatalf("groups %+v", sync.Groups)
	}
	if len(sync.Quests) != 3 {
		t.Fatalf("want 3 starters, got %d", len(sync.Quests))
	}
	var participating bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT participating FROM characters WHERE player_id=$1 AND name='Magni'`, player.ID).Scan(&participating); err != nil || !participating {
		t.Fatalf("participating=%v err=%v", participating, err)
	}

	resp, err = api.do(http.MethodGet, "/v1/sync", nil, &sync)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("second sync %d %v", statusOf(resp), err)
	}
	if len(sync.Groups) != 1 || len(sync.Quests) != 3 {
		t.Fatalf("not idempotent groups=%d quests=%d", len(sync.Groups), len(sync.Quests))
	}
	titles := map[string]bool{}
	for _, q := range sync.Quests {
		titles[q.Title] = true
		if q.CreatedBy != "Sagalore" {
			t.Fatalf("starter %q created_by=%q want Sagalore", q.Title, q.CreatedBy)
		}
		if !q.HasImage {
			t.Fatalf("starter %q missing cover image", q.Title)
		}
	}
	for _, want := range []string{"Wave hello", "A quick rhythm", "Strike a pose"} {
		if !titles[want] {
			t.Fatalf("missing starter %q in %+v", want, titles)
		}
	}

	resp, err = api.do(http.MethodPost, "/v1/groups/"+sync.Groups[0].ID+"/invites", map[string]string{
		"name": "Other", "realm": "Everlook",
	}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("invite personal %d %v", statusOf(resp), err)
	}
	resp, err = api.do(http.MethodPost, "/v1/groups/"+sync.Groups[0].ID+"/leave", nil, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("leave personal %d %v", statusOf(resp), err)
	}
}

func TestPersonalAutoIncludesWithoutPrompt(t *testing.T) {
	resetDB(t)
	store := NewStore(testPool)
	srv := httptest.NewServer((&Server{store: store}).Handler())
	t.Cleanup(srv.Close)
	player, token := devSession(t, "bnet-include", "Include#1", "Include")
	api := apiClient{base: srv.URL, token: token, http: srv.Client()}
	if _, err := store.UpsertCharacter(context.Background(), player.ID, contract.CharacterUpsert{
		Name: "Magni", Realm: "Everlook", Level: 5, BattleTag: "Include#1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsurePersonalGroup(context.Background(), player.ID); err != nil {
		t.Fatal(err)
	}
	var groupID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text FROM groups WHERE personal AND owner_player_id=$1`, player.ID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE characters SET participating=false WHERE player_id=$1`, player.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO group_invites (group_id, character_name, realm, status)
		VALUES ($1::uuid, 'Magni', 'Everlook', 'pending')`, groupID); err != nil {
		t.Fatal(err)
	}

	var sync contract.SyncResponse
	resp, err := api.do(http.MethodGet, "/v1/sync", nil, &sync)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sync %d %v", statusOf(resp), err)
	}
	for _, inv := range sync.Invites {
		if inv.GroupID == groupID {
			t.Fatalf("personal include invite still pending: %+v", inv)
		}
	}
	var participating bool
	if err := testPool.QueryRow(context.Background(), `
		SELECT participating FROM characters WHERE player_id=$1 AND name='Magni'`, player.ID).Scan(&participating); err != nil || !participating {
		t.Fatalf("participating=%v err=%v", participating, err)
	}
}

func TestUpgradePersonalStartersV2(t *testing.T) {
	resetDB(t)
	store := NewStore(testPool)
	player, token := devSession(t, "bnet-upgrade", "Upgrade#1", "Upgrade")
	if _, err := store.UpsertCharacter(context.Background(), player.ID, contract.CharacterUpsert{
		Name: "Magni", Realm: "Everlook", Level: 5, BattleTag: "Upgrade#1",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsurePersonalGroup(context.Background(), player.ID); err != nil {
		t.Fatal(err)
	}
	var groupID string
	if err := testPool.QueryRow(context.Background(), `
		SELECT id::text FROM groups WHERE personal AND owner_player_id=$1`, player.ID).Scan(&groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE quests SET active=false WHERE group_id=$1::uuid`, groupID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.EnsurePlayer(context.Background(), personalCreator, "Sagalore"); err != nil {
		t.Fatal(err)
	}
	for _, title := range []string{"Say hello", "Take a seat", "Celebrate"} {
		if _, err := store.CreateQuest(context.Background(), QuestInput{
			Title: title, PartyQuest: true, GroupID: groupID, CreatedBy: personalCreator,
			Objectives: []contract.Objective{
				{ID: "wave", Type: contract.ObjectiveEmote, Text: "Wave", Emote: "WAVE"},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	srv := httptest.NewServer((&Server{store: store}).Handler())
	t.Cleanup(srv.Close)
	api := apiClient{base: srv.URL, token: token, http: srv.Client()}

	var sync contract.SyncResponse
	resp, err := api.do(http.MethodGet, "/v1/sync", nil, &sync)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sync %d %v", statusOf(resp), err)
	}
	if len(sync.Quests) != 3 {
		t.Fatalf("want 3 upgraded starters, got %d", len(sync.Quests))
	}
	for _, q := range sync.Quests {
		if q.Title == "Say hello" || q.Title == "Take a seat" || q.Title == "Celebrate" {
			t.Fatalf("old starter still active: %s", q.Title)
		}
		if q.Title == "A quick rhythm" {
			raw, _ := json.Marshal(q.Objectives)
			if strings.Contains(string(raw), `"SIT"`) {
				t.Fatalf("rhythm still uses silent /sit: %s", raw)
			}
			if !strings.Contains(string(raw), `"KNEEL"`) {
				t.Fatalf("rhythm missing /kneel: %s", raw)
			}
		}
	}
}

func TestSyncPreloadsPendingJoinSagas(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool)}).Handler())
	t.Cleanup(srv.Close)
	owner, ownerToken := devSession(t, "bnet-owner", "Owner#1", "Owner")
	_, guestToken := devSession(t, "bnet-guest", "Guest#9", "Guest")
	group := devGroup(t, owner.ID, "gutar", "Gutar")
	ownerAPI := apiClient{base: srv.URL, token: ownerToken, http: srv.Client()}
	guestAPI := apiClient{base: srv.URL, token: guestToken, http: srv.Client()}

	var saga contract.Quest
	resp, err := ownerAPI.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Friend saga", "party": true, "group": "gutar",
		"objectives": []map[string]any{{"id": "x", "type": "talk", "text": "Talk", "npc": "Farley"}},
	}, &saga)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create saga %d %v", statusOf(resp), err)
	}

	var invite contract.Invite
	resp, err = ownerAPI.do(http.MethodPost, "/v1/groups/"+group.ID+"/invites", map[string]string{
		"name": "Magni", "realm": "Everlook", "battletag": "Guest#9",
	}, &invite)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("invite %d %v", statusOf(resp), err)
	}

	var sync contract.SyncResponse
	resp, err = guestAPI.do(http.MethodGet, "/v1/sync", nil, &sync)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("guest sync %d %v", statusOf(resp), err)
	}
	personal := 0
	pending := 0
	for _, q := range sync.Quests {
		if q.PendingInviteID == invite.ID {
			pending++
			if q.ID != saga.ID {
				t.Fatalf("pending quest %+v", q)
			}
		}
		if q.PendingInviteID == "" {
			personal++
		}
	}
	if personal != 3 || pending != 1 {
		t.Fatalf("personal=%d pending=%d quests=%d", personal, pending, len(sync.Quests))
	}
	if len(sync.Invites) != 1 || sync.Invites[0].ID != invite.ID {
		t.Fatalf("invites %+v", sync.Invites)
	}

	var accepted contract.Invite
	resp, err = guestAPI.do(http.MethodPost, "/v1/invites/"+invite.ID+"/accept", map[string]string{}, &accepted)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("accept %d %v", statusOf(resp), err)
	}
	var memberCount int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM group_members WHERE group_id=$1`, group.ID).Scan(&memberCount); err != nil {
		t.Fatal(err)
	}
	if memberCount < 2 {
		t.Fatalf("after accept members=%d accepted=%+v", memberCount, accepted)
	}
	sync = contract.SyncResponse{}
	resp, err = guestAPI.do(http.MethodGet, "/v1/sync", nil, &sync)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("after accept %d %v", statusOf(resp), err)
	}
	found := false
	for _, q := range sync.Quests {
		if q.ID == saga.ID {
			found = true
			if q.PendingInviteID != "" {
				t.Fatalf("still pending quest=%+v invites=%+v groups=%+v", q, sync.Invites, sync.Groups)
			}
		}
	}
	if !found {
		t.Fatal("friend saga missing after accept")
	}
	if len(sync.Groups) < 2 {
		t.Fatalf("want personal+gutar, got %+v", sync.Groups)
	}
}