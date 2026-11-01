package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestLeaderboardAndActivitySort(t *testing.T) {
	resetDB(t)
	leaderboardNow = func() time.Time {
		return time.Date(2026, 10, 29, 15, 0, 0, 0, time.UTC)
	}
	t.Cleanup(func() { leaderboardNow = time.Now })

	srv := httptest.NewServer((&Server{store: NewStore(testPool), adminToken: "admin-test"}).Handler())
	t.Cleanup(srv.Close)
	admin := apiClient{base: srv.URL, token: "admin-test", http: srv.Client()}

	magnus, magnusToken := devSession(t, "bnet-magnus", "Magnus#2231", "Magnus")
	miraPlayer, miraToken := devSession(t, "bnet-mira", "Mira#1", "Mira")
	brannPlayer, brannToken := devSession(t, "bnet-brann", "Brann#1", "Brann")
	_, strangerToken := devSession(t, "bnet-stranger", "Stranger#9", "Stranger")
	group := devGroup(t, magnus.ID, "tutorial", "Tutorial")
	joinGroup(t, group.ID, miraPlayer.ID)
	joinGroup(t, group.ID, brannPlayer.ID)

	author := apiClient{base: srv.URL, token: magnusToken, http: srv.Client()}
	mira := apiClient{base: srv.URL, token: miraToken, http: srv.Client()}
	brann := apiClient{base: srv.URL, token: brannToken, http: srv.Client()}
	stranger := apiClient{base: srv.URL, token: strangerToken, http: srv.Client()}

	var magni, miraChar, brannChar contract.Character
	resp, err := admin.do(http.MethodPost, "/v1/admin/characters", map[string]any{
		"player_id": magnus.ID, "name": "Magni", "realm": "Celebras",
		"race": "Dwarf", "class": "Warrior", "level": 42,
	}, &magni)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("magni %d %v", statusOf(resp), err)
	}
	resp, err = admin.do(http.MethodPost, "/v1/admin/characters", map[string]any{
		"player_id": miraPlayer.ID, "name": "Mira", "realm": "Celebras",
		"race": "Human", "class": "Mage", "level": 40,
	}, &miraChar)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("mira %d %v", statusOf(resp), err)
	}
	resp, err = admin.do(http.MethodPost, "/v1/admin/characters", map[string]any{
		"player_id": brannPlayer.ID, "name": "Brann", "realm": "Celebras",
		"race": "Dwarf", "class": "Hunter", "level": 38,
	}, &brannChar)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("brann %d %v", statusOf(resp), err)
	}

	makeSaga := func(title string) contract.Quest {
		t.Helper()
		var q contract.Quest
		resp, err := author.do(http.MethodPost, "/v1/sagas", map[string]any{
			"title": title, "description": title,
			"objectives": []map[string]any{{"id": "k", "type": "kill", "text": "Drop 5 rabbits", "npc": "Rabbit", "count": 5}},
			"party":      true,
		}, &q)
		if err != nil || resp.StatusCode != http.StatusCreated {
			t.Fatalf("saga %s %d %v", title, statusOf(resp), err)
		}
		return q
	}
	sagaA := makeSaga("Rabbits")
	sagaB := makeSaga("Boars")
	sagaC := makeSaga("Wolves")

	finish := func(client apiClient, character, quest, completed, started string) contract.CompletionResponse {
		t.Helper()
		var out contract.CompletionResponse
		body := contract.CompletionRequest{
			CharacterID: character, QuestID: quest, QuestVersion: 1,
			CompletedAt: completed, StartedAt: started,
		}
		resp, err := client.do(http.MethodPost, "/v1/complete", body, &out)
		if err != nil || resp.StatusCode != http.StatusOK || !out.Completed || out.AlreadyCompleted {
			t.Fatalf("complete %+v %d %v", out, statusOf(resp), err)
		}
		return out
	}
	first := finish(author, magni.ID, sagaA.ID, "2026-10-29T12:00:00Z", "2026-10-29T11:00:00Z")
	if first.DurationSec == nil || *first.DurationSec != 3600 || first.StartedAt == "" {
		t.Fatalf("duration %+v", first)
	}
	var again contract.CompletionResponse
	resp, err = author.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: sagaA.ID, QuestVersion: 1,
		CompletedAt: "2026-10-29T13:00:00Z", StartedAt: "2026-10-29T12:00:00Z",
	}, &again)
	if err != nil || !again.AlreadyCompleted {
		t.Fatalf("repeat %+v %v", again, err)
	}
	var rows int
	if err := testPool.QueryRow(context.Background(), `
		SELECT count(*) FROM progress WHERE character_id=$1 AND quest_id=$2`, magni.ID, sagaA.ID).Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("rows %d %v", rows, err)
	}
	finish(author, magni.ID, sagaB.ID, "2026-10-22T12:00:00Z", "2026-10-22T11:30:00Z")
	late := finish(author, magni.ID, sagaC.ID, "2026-09-15T12:00:00Z", "2026-09-15T13:00:00Z")
	if late.DurationSec != nil {
		t.Fatalf("negative duration stored %+v", late.DurationSec)
	}
	finish(mira, miraChar.ID, sagaA.ID, "2026-10-29T12:30:00Z", "2026-10-29T12:00:00Z")
	finish(brann, brannChar.ID, sagaA.ID, "2026-10-29T10:00:00Z", "")

	var bad contract.ErrorBody
	resp, err = author.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: sagaA.ID, QuestVersion: 1, StartedAt: "yesterday",
	}, &bad)
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad start %d %+v %v", statusOf(resp), bad, err)
	}

	// A second finish row is allowed by the schema. The API does not write one yet.
	if _, err := testPool.Exec(context.Background(), `
		INSERT INTO progress (character_id, quest_id, quest_version, status, completed_at, duration_sec)
		VALUES ($1,$2,1,'completed',$3,15)`, magni.ID, sagaA.ID, time.Date(2026, 10, 29, 18, 0, 0, 0, time.UTC)); err != nil {
		t.Fatalf("history insert: %v", err)
	}

	token, err := NewStore(testPool).MintPersonalToken(context.Background(), miraPlayer.ID, "board")
	if err != nil {
		t.Fatal(err)
	}
	gst := apiClient{base: srv.URL, token: token.Token, http: srv.Client()}

	var board contract.Leaderboard
	resp, err = gst.do(http.MethodGet, "/v1/groups/"+group.ID+"/leaderboard?period=all", nil, &board)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("board %d %v", statusOf(resp), err)
	}
	if board.Period != "all" || board.GroupName != "Tutorial" || len(board.Entries) != 3 {
		t.Fatalf("%+v", board)
	}
	if board.Entries[0].Name != "Magni" || board.Entries[0].Completed != 3 || board.Entries[0].Rank != 1 {
		t.Fatalf("rank %+v", board.Entries)
	}
	if board.Entries[0].Class != "Warrior" || board.Entries[0].Race != "Dwarf" || board.Entries[0].Level != 42 || board.Entries[0].Realm != "Celebras" {
		t.Fatalf("identity %+v", board.Entries[0])
	}
	// Same count: the earlier last completion ranks higher, so Brann is above Mira.
	if board.Entries[1].Name != "Brann" || board.Entries[1].Completed != 1 || board.Entries[2].Name != "Mira" {
		t.Fatalf("order %+v", board.Entries)
	}

	resp, err = author.do(http.MethodGet, "/v1/groups/"+group.ID+"/leaderboard?period=today", nil, &board)
	if err != nil || resp.StatusCode != http.StatusOK || len(board.Entries) != 3 {
		t.Fatalf("today %+v %d %v", board, statusOf(resp), err)
	}
	for _, e := range board.Entries {
		if e.Completed != 1 {
			t.Fatalf("today count %+v", board.Entries)
		}
	}
	// Same count: earlier last completion ranks first. Brann 10:00, Magni 12:00 (history max is 18:00).
	if board.Entries[0].Name != "Brann" || board.Entries[2].Name != "Magni" {
		t.Fatalf("today order %+v", board.Entries)
	}

	resp, err = author.do(http.MethodGet, "/v1/groups/"+group.ID+"/leaderboard?period=week", nil, &board)
	if err != nil || len(board.Entries) != 3 || board.Entries[0].Completed != 1 {
		t.Fatalf("week %+v %v", board.Entries, err)
	}
	resp, err = author.do(http.MethodGet, "/v1/groups/"+group.ID+"/leaderboard?period=month", nil, &board)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("month %d %v", statusOf(resp), err)
	}
	if len(board.Entries) != 3 || board.Entries[0].Name != "Magni" || board.Entries[0].Completed != 2 {
		t.Fatalf("month %+v", board.Entries)
	}

	resp, err = author.do(http.MethodGet, "/v1/groups/"+group.ID+"/leaderboard?period=year", nil, &bad)
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad period %d %+v %v", statusOf(resp), bad, err)
	}
	resp, err = stranger.do(http.MethodGet, "/v1/groups/"+group.ID+"/leaderboard", nil, &bad)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("outsider %d %+v %v", statusOf(resp), bad, err)
	}

	var detail contract.QuestDetail
	resp, err = mira.do(http.MethodGet, "/v1/sagas/"+sagaA.ID+"?sort=completion_time", nil, &detail)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("activity %d %v", statusOf(resp), err)
	}
	if len(detail.CompletedBy) != 4 {
		t.Fatalf("activity rows %d %+v", len(detail.CompletedBy), detail.CompletedBy)
	}
	// Fastest first, missing duration last. Mira 1800, history row 15, Magni 3600, Brann nil.
	if detail.CompletedBy[0].Character != "Magni" || detail.CompletedBy[0].DurationSec == nil || *detail.CompletedBy[0].DurationSec != 15 {
		t.Fatalf("fastest %+v", detail.CompletedBy)
	}
	if detail.CompletedBy[1].Character != "Mira" || *detail.CompletedBy[1].DurationSec != 1800 {
		t.Fatalf("second %+v", detail.CompletedBy[1])
	}
	if detail.CompletedBy[2].Character != "Magni" || *detail.CompletedBy[2].DurationSec != 3600 {
		t.Fatalf("third %+v", detail.CompletedBy[2])
	}
	if detail.CompletedBy[3].Character != "Brann" || detail.CompletedBy[3].DurationSec != nil {
		t.Fatalf("null last %+v", detail.CompletedBy[3])
	}
	if detail.Card == nil || len(detail.Card.Rows) != 4 {
		t.Fatalf("card %+v", detail.Card)
	}

	resp, err = mira.do(http.MethodGet, "/v1/sagas/"+sagaA.ID+"?sort=completed_at", nil, &detail)
	if err != nil || resp.StatusCode != http.StatusOK || detail.CompletedBy[0].CompletedAt < detail.CompletedBy[1].CompletedAt {
		t.Fatalf("newest %+v %v", detail.CompletedBy, err)
	}
	resp, err = mira.do(http.MethodGet, "/v1/sagas/"+sagaA.ID+"?sort=points", nil, &bad)
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad sort %d %+v %v", statusOf(resp), bad, err)
	}
	resp, err = stranger.do(http.MethodGet, "/v1/sagas/"+sagaA.ID, nil, &bad)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("stranger saga %d %+v %v", statusOf(resp), bad, err)
	}

	var syncOut contract.SyncResponse
	resp, err = author.do(http.MethodGet, "/v1/sync", nil, &syncOut)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("sync %d %v", statusOf(resp), err)
	}
	// One set of period boards per group the player belongs to (Personal + the test group).
	if len(syncOut.Leaderboards) != 8 {
		t.Fatalf("boards %d", len(syncOut.Leaderboards))
	}
	var sawFast bool
	for _, c := range syncOut.Completions {
		if c.Character == "Mira" && c.QuestID == sagaA.ID && c.DurationSec != nil && *c.DurationSec == 1800 {
			sawFast = true
		}
	}
	if !sawFast {
		t.Fatalf("sync completions %+v", syncOut.Completions)
	}
}
