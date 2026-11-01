package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestMemberPatchIsAuthorOnly(t *testing.T) {
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

	var quest contract.Quest
	resp, err := author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Owned by Magnus", "description": "Only the author.",
		"objectives": []map[string]any{{"id": "x", "type": "talk", "text": "Talk", "npc": "Farley"}},
		"party":      true,
	}, &quest)
	if err != nil || resp.StatusCode != http.StatusCreated || quest.CreatedBy != "Magnus#2231" {
		t.Fatalf("create %d %v %+v", statusOf(resp), err, quest)
	}

	resp, err = member.do(http.MethodPatch, "/v1/sagas/"+quest.ID, map[string]any{
		"title": "Hijack", "changes": []string{"no"},
	}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("other member %d %v", statusOf(resp), err)
	}

	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+quest.ID, map[string]any{
		"title": "Updated", "created_by": "Other#1", "changes": []string{"New name"},
	}, nil)
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("created_by %d %v", statusOf(resp), err)
	}

	var ok contract.Quest
	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+quest.ID, map[string]any{
		"title": "Updated", "changes": []string{"New name"},
	}, &ok)
	if err != nil || resp.StatusCode != http.StatusOK || ok.Title != "Updated" || ok.Version != 2 {
		t.Fatalf("author patch %+v %d %v", ok, statusOf(resp), err)
	}

	resp, err = admin.do(http.MethodPatch, "/v1/admin/sagas/"+quest.ID, map[string]any{
		"title": "Admin override", "changes": []string{"Admin"},
	}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("admin write %d %v", statusOf(resp), err)
	}
}

func TestNonMemberCreateIsForbidden(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool), adminToken: "admin-test"}).Handler())
	t.Cleanup(srv.Close)
	owner, _ := devSession(t, "bnet-owner", "Owner#1", "Owner")
	devGroup(t, owner.ID, "tutorial", "Tutorial")
	_, strangerToken := devSession(t, "bnet-stranger", "Stranger#1", "Stranger")
	stranger := apiClient{base: srv.URL, token: strangerToken, http: srv.Client()}
	resp, err := stranger.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Nope", "description": "Outsider.",
		"objectives": []map[string]any{{"id": "x", "type": "talk", "text": "Talk", "npc": "Farley"}},
		"party":      true, "group": "tutorial",
	}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("non-member create %d %v", statusOf(resp), err)
	}
}
