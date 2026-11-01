package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestBootstrapGroupsExistingRowsOnly(t *testing.T) {
	resetDB(t)
	store := NewStore(testPool)
	ctx := context.Background()
	if err := store.Bootstrap(ctx, "tutorial:Tutorial", "", "https://hooks.example/example"); err != nil {
		t.Fatal(err)
	}
	var groups int
	if err := testPool.QueryRow(ctx, `SELECT count(*) FROM groups`).Scan(&groups); err != nil || groups != 0 {
		t.Fatalf("fresh database invented %d groups (%v)", groups, err)
	}

	player, err := store.MergeBattleNet(ctx, "bnet-1", "Player#1234", "Player")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateCharacter(ctx, CharacterInput{PlayerID: player.ID, Name: "Magni", Realm: "Everlook", Level: 4}); err != nil {
		t.Fatal(err)
	}
	quest, err := store.CreateQuest(ctx, QuestInput{
		Title: "A walk", Description: "Go outside.", CreatedBy: "Player#1234", PartyQuest: true,
		Objectives: []contract.Objective{{ID: "go", Type: "go", Text: "Stand in Goldshire", Zone: "Elwynn Forest"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.AddLog(ctx, player.ID, "", quest.ID, "note", "Note", "Wrote it down."); err != nil {
		t.Fatal(err)
	}
	if err := store.Bootstrap(ctx, "", "", ""); err == nil {
		t.Fatal("expected startup to refuse unscoped sagas")
	}
	if err := store.Bootstrap(ctx, "tutorial:Tutorial", "Player#1234", "https://hooks.example/example"); err != nil {
		t.Fatal(err)
	}
	var groupID, slug, hook string
	var sourced bool
	if err := testPool.QueryRow(ctx, `SELECT id::text, slug, webhook_url, webhook_sourced FROM groups`).Scan(&groupID, &slug, &hook, &sourced); err != nil {
		t.Fatal(err)
	}
	if slug != "tutorial" || hook == "" || !sourced {
		t.Fatalf("group %s hook %q sourced %v", slug, hook, sourced)
	}
	var attached, participating, members int
	_ = testPool.QueryRow(ctx, `SELECT count(*) FROM quests WHERE group_id=$1`, groupID).Scan(&attached)
	_ = testPool.QueryRow(ctx, `SELECT count(*) FROM adventure_log WHERE group_id=$1`, groupID).Scan(&attached)
	var logAttached int
	_ = testPool.QueryRow(ctx, `SELECT count(*) FROM quests WHERE group_id=$1`, groupID).Scan(&attached)
	_ = testPool.QueryRow(ctx, `SELECT count(*) FROM adventure_log WHERE group_id=$1`, groupID).Scan(&logAttached)
	_ = testPool.QueryRow(ctx, `SELECT count(*) FROM characters WHERE participating`).Scan(&participating)
	_ = testPool.QueryRow(ctx, `SELECT count(*) FROM group_members WHERE role='owner' AND player_id=$1`, player.ID).Scan(&members)
	if attached != 1 || logAttached != 1 || participating != 1 || members != 1 {
		t.Fatalf("attached %d log %d participating %d owner %d", attached, logAttached, participating, members)
	}
	if err := store.Bootstrap(ctx, "tutorial:Tutorial", "", "https://hooks.example/other"); err != nil {
		t.Fatal(err)
	}
	var hook2 string
	_ = testPool.QueryRow(ctx, `SELECT webhook_url FROM groups WHERE id=$1`, groupID).Scan(&hook2)
	if hook2 != hook {
		t.Fatalf("webhook recopied: %s", hook2)
	}
}

func TestSyncIsolationBetweenGroups(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool)}).Handler())
	t.Cleanup(srv.Close)
	a, aToken := devSession(t, "bnet-a", "Alpha#1", "Alpha")
	b, bToken := devSession(t, "bnet-b", "Beta#2", "Beta")
	devGroup(t, a.ID, "alpha", "Alpha")
	devGroup(t, b.ID, "beta", "Beta")
	authorA := apiClient{base: srv.URL, token: aToken, http: srv.Client()}
	authorB := apiClient{base: srv.URL, token: bToken, http: srv.Client()}
	var sagaA, sagaB contract.Quest
	resp, err := authorA.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Alpha saga", "party": true,
		"objectives": []map[string]any{{"id": "x", "type": "talk", "text": "Talk", "npc": "Farley"}},
	}, &sagaA)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("a %d %v", statusOf(resp), err)
	}
	resp, err = authorB.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Beta saga", "party": true,
		"objectives": []map[string]any{{"id": "x", "type": "talk", "text": "Talk", "npc": "Farley"}},
	}, &sagaB)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("b %d %v", statusOf(resp), err)
	}
	var syncA, syncB contract.SyncResponse
	resp, err = authorA.do(http.MethodGet, "/v1/sync", nil, &syncA)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sync A %d %v", statusOf(resp), err)
	}
	if !syncHasQuest(syncA, sagaA.ID) || syncHasQuest(syncA, sagaB.ID) {
		t.Fatalf("sync A leaked or missing %+v", questIDs(syncA))
	}
	resp, err = authorB.do(http.MethodGet, "/v1/sync", nil, &syncB)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sync B %d %v", statusOf(resp), err)
	}
	if !syncHasQuest(syncB, sagaB.ID) || syncHasQuest(syncB, sagaA.ID) {
		t.Fatalf("sync B leaked or missing %+v", questIDs(syncB))
	}
}

func syncHasQuest(sync contract.SyncResponse, id string) bool {
	for _, q := range sync.Quests {
		if q.ID == id {
			return true
		}
	}
	return false
}

func questIDs(sync contract.SyncResponse) []string {
	out := make([]string, 0, len(sync.Quests))
	for _, q := range sync.Quests {
		out = append(out, q.ID)
	}
	return out
}

func TestListGroupMembers(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool)}).Handler())
	t.Cleanup(srv.Close)
	owner, ownerToken := devSession(t, "bnet-owner", "Owner#1", "Owner")
	group := devGroup(t, owner.ID, "tutorial", "Tutorial")
	_, guestToken := devSession(t, "bnet-guest", "Guest#9", "Guest")
	ownerAPI := apiClient{base: srv.URL, token: ownerToken, http: srv.Client()}
	guestAPI := apiClient{base: srv.URL, token: guestToken, http: srv.Client()}

	var listed struct {
		Members []contract.GroupMemberRow `json:"members"`
	}
	resp, err := ownerAPI.do(http.MethodGet, "/v1/groups/"+group.ID+"/members", nil, &listed)
	if err != nil || resp.StatusCode != http.StatusOK || len(listed.Members) != 1 || listed.Members[0].Role != "owner" {
		t.Fatalf("owner list %d %v %+v", statusOf(resp), err, listed.Members)
	}
	resp, err = guestAPI.do(http.MethodGet, "/v1/groups/"+group.ID+"/members", nil, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("stranger %d %v", statusOf(resp), err)
	}
}

func TestInvitesJoinDeclineAndPartyCredit(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool)}).Handler())
	t.Cleanup(srv.Close)
	owner, ownerToken := devSession(t, "bnet-owner", "Owner#1", "Owner")
	group := devGroup(t, owner.ID, "tutorial", "Tutorial")
	guest, guestToken := devSession(t, "bnet-guest", "Guest#9", "Guest")
	ownerAPI := apiClient{base: srv.URL, token: ownerToken, http: srv.Client()}
	guestAPI := apiClient{base: srv.URL, token: guestToken, http: srv.Client()}

	var byTag contract.Invite
	resp, err := ownerAPI.do(http.MethodPost, "/v1/groups/"+group.ID+"/invites", map[string]string{
		"name": "Magni", "realm": "Everlook", "battletag": "Guest#9",
	}, &byTag)
	if err != nil || resp.StatusCode != http.StatusCreated || byTag.ID == "" {
		t.Fatalf("invite tag %d %v %+v", statusOf(resp), err, byTag)
	}
	resp, err = guestAPI.do(http.MethodPost, "/v1/invites/"+byTag.ID+"/accept", map[string]string{}, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("accept %d %v", statusOf(resp), err)
	}
	var participating bool
	var role string
	if err := testPool.QueryRow(context.Background(), `
		SELECT c.participating, gm.role FROM characters c
		JOIN group_members gm ON gm.player_id=c.player_id AND gm.group_id=$2
		WHERE c.player_id=$1 AND c.name='Magni'`, guest.ID, group.ID).Scan(&participating, &role); err != nil || !participating || role != "member" {
		t.Fatalf("joined participating=%v role=%s err=%v", participating, role, err)
	}

	var declined contract.Invite
	resp, err = ownerAPI.do(http.MethodPost, "/v1/groups/"+group.ID+"/invites", map[string]string{
		"name": "Mira", "realm": "Everlook",
	}, &declined)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatal(statusOf(resp), err)
	}
	// Character-addressed invite is accepted only by the account that owns that character,
	// or by anyone when the character does not exist yet. Decline sticks.
	if _, err := NewStore(testPool).UpsertCharacter(context.Background(), guest.ID, contract.CharacterUpsert{
		Name: "Mira", Realm: "Everlook", Level: 3, BattleTag: "Guest#9",
	}); err != nil {
		t.Fatal(err)
	}
	resp, err = guestAPI.do(http.MethodPost, "/v1/invites/"+declined.ID+"/decline", map[string]string{}, nil)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("decline %d %v", statusOf(resp), err)
	}
	resp, err = guestAPI.do(http.MethodPost, "/v1/invites/"+declined.ID+"/accept", map[string]string{}, nil)
	if err != nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("decline should stick %d %v", statusOf(resp), err)
	}

	stranger, strangerToken := devSession(t, "bnet-stranger", "Stranger#1", "Stranger")
	_ = stranger
	strangerAPI := apiClient{base: srv.URL, token: strangerToken, http: srv.Client()}
	var saga contract.Quest
	resp, err = ownerAPI.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Together", "party": true,
		"objectives": []map[string]any{{"id": "x", "type": "talk", "text": "Talk", "npc": "Farley"}},
	}, &saga)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatal(statusOf(resp), err)
	}
	var ch contract.Character
	_ = testPool.QueryRow(context.Background(), `SELECT id::text FROM characters WHERE name='Magni'`).Scan(&ch.ID)
	resp, err = strangerAPI.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: ch.ID, QuestID: saga.ID, QuestVersion: 1, BattleTag: "Stranger#1", Level: 4,
	}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-member complete %d %v", statusOf(resp), err)
	}
}
