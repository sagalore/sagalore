package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestDeleteSagaOnlyEmptyDrafts(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool), adminToken: "admin-test"}).Handler())
	t.Cleanup(srv.Close)
	magnus, magnusToken := devSession(t, "bnet-magnus", "Magnus#2231", "Magnus")
	other, otherToken := devSession(t, "bnet-other", "Other#1", "Other")
	group := devGroup(t, magnus.ID, "tutorial", "Tutorial")
	joinGroup(t, group.ID, other.ID)
	author := apiClient{base: srv.URL, token: magnusToken, http: srv.Client()}
	member := apiClient{base: srv.URL, token: otherToken, http: srv.Client()}
	admin := apiClient{base: srv.URL, token: "admin-test", http: srv.Client()}

	create := func(title string, active bool) contract.Quest {
		var q contract.Quest
		resp, err := author.do(http.MethodPost, "/v1/sagas", map[string]any{
			"title": title, "description": "d", "active": active, "party": true,
			"objectives": []map[string]any{{"id": "x", "type": "talk", "text": "Talk", "npc": "Farley"}},
		}, &q)
		if err != nil || resp.StatusCode != http.StatusCreated {
			t.Fatalf("create %s %d %v", title, statusOf(resp), err)
		}
		return q
	}
	live := create("Live", true)
	draft := create("Draft", false)
	played := create("Played draft", false)

	var char contract.Character
	resp, err := admin.do(http.MethodPost, "/v1/admin/characters", map[string]any{
		"player_id": magnus.ID, "name": "Magni", "realm": "Celebras", "race": "Dwarf", "class": "Warrior", "level": 10,
	}, &char)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("char %d %v", statusOf(resp), err)
	}
	if _, err := testPool.Exec(context.Background(),
		`INSERT INTO progress (character_id, quest_id, quest_version, status) VALUES ($1,$2,1,'active')`,
		char.ID, played.ID); err != nil {
		t.Fatal(err)
	}

	if resp, err = member.do(http.MethodDelete, "/v1/sagas/"+draft.ID, nil, nil); err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-author %d %v", statusOf(resp), err)
	}
	var e contract.ErrorBody
	if resp, err = author.do(http.MethodDelete, "/v1/sagas/"+live.ID, nil, &e); err != nil || resp.StatusCode != http.StatusConflict {
		t.Fatalf("live %d %v", statusOf(resp), err)
	}
	if e.Error != "not_deletable" || !strings.Contains(e.Message, "archive") {
		t.Fatalf("live error %+v", e)
	}
	if resp, err = author.do(http.MethodDelete, "/v1/sagas/"+played.ID, nil, &e); err != nil || resp.StatusCode != http.StatusConflict || e.Error != "not_deletable" {
		t.Fatalf("played %d %v %+v", statusOf(resp), err, e)
	}
	if resp, err = author.do(http.MethodDelete, "/v1/sagas/00000000-0000-4000-8000-000000000000", nil, nil); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("missing %d %v", statusOf(resp), err)
	}
	if resp, err = author.do(http.MethodDelete, "/v1/sagas/"+draft.ID, nil, nil); err != nil || resp.StatusCode != http.StatusNoContent {
		t.Fatalf("draft %d %v", statusOf(resp), err)
	}
	if resp, err = author.do(http.MethodGet, "/v1/sagas/"+draft.ID, nil, nil); err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("draft still there %d %v", statusOf(resp), err)
	}
	for _, id := range []string{live.ID, played.ID} {
		if resp, err = author.do(http.MethodGet, "/v1/sagas/"+id, nil, nil); err != nil || resp.StatusCode != http.StatusOK {
			t.Fatalf("kept %s %d %v", id, statusOf(resp), err)
		}
	}
}
