package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestCharacterWritesComeFromSync(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool), adminToken: "admin-test"}).Handler())
	t.Cleanup(srv.Close)
	admin := apiClient{base: srv.URL, token: "admin-test", http: srv.Client()}
	ownerRec, ownerToken := devSession(t, "bnet-owner", "Owner#1", "Owner")
	_, otherToken := devSession(t, "bnet-other", "Other#2", "Other")
	owner := apiClient{base: srv.URL, token: ownerToken, http: srv.Client()}
	other := apiClient{base: srv.URL, token: otherToken, http: srv.Client()}

	var denied contract.ErrorBody
	resp, err := admin.do(http.MethodPost, "/v1/admin/characters", map[string]any{
		"player_id": ownerRec.ID, "name": "Magni", "realm": "Everlook",
		"race": "Dwarf", "class": "Paladin", "level": 12,
	}, &denied)
	if err != nil || resp.StatusCode != http.StatusForbidden || denied.Error != "character_readonly" {
		t.Fatalf("admin create %d %+v %v", statusOf(resp), denied, err)
	}

	ch := syncCharacter(t, owner, "Magni", "Everlook", "Dwarf", "Paladin", 12)
	if ch.Level != 12 || ch.Name != "Magni" {
		t.Fatalf("sync %+v", ch)
	}

	resp, err = admin.do(http.MethodPatch, "/v1/admin/characters/"+ch.ID, map[string]any{"level": 80}, &denied)
	if err != nil || resp.StatusCode != http.StatusForbidden || denied.Error != "character_readonly" {
		t.Fatalf("admin patch %d %+v %v", statusOf(resp), denied, err)
	}
	resp, err = admin.do(http.MethodDelete, "/v1/admin/characters/"+ch.ID, nil, &denied)
	if err != nil || resp.StatusCode != http.StatusForbidden || denied.Error != "character_readonly" {
		t.Fatalf("admin delete %d %+v %v", statusOf(resp), denied, err)
	}

	var listed struct {
		Characters []contract.Character `json:"characters"`
	}
	resp, err = admin.do(http.MethodGet, "/v1/admin/characters", nil, &listed)
	if err != nil || resp.StatusCode != http.StatusOK || len(listed.Characters) != 1 {
		t.Fatalf("admin list %d %+v %v", statusOf(resp), listed, err)
	}

	var patched contract.Character
	resp, err = owner.do(http.MethodPatch, "/v1/characters/"+ch.ID, map[string]any{
		"level": 40, "name": "NotMagni",
	}, &denied)
	if err != nil || resp.StatusCode != http.StatusForbidden || denied.Error != "character_readonly" {
		t.Fatalf("owner identity patch %d %+v %v", statusOf(resp), denied, err)
	}

	resp, err = owner.do(http.MethodPatch, "/v1/characters/"+ch.ID, map[string]any{
		"backstory": "Left Ironforge with a hammer.",
	}, &patched)
	if err != nil || resp.StatusCode != http.StatusOK || patched.Backstory != "Left Ironforge with a hammer." || patched.Level != 12 || patched.Name != "Magni" {
		t.Fatalf("backstory %+v %d %v", patched, statusOf(resp), err)
	}

	resp, err = other.do(http.MethodPatch, "/v1/characters/"+ch.ID, map[string]any{
		"backstory": "stolen",
	}, &denied)
	if err != nil || resp.StatusCode != http.StatusNotFound {
		t.Fatalf("other owner %d %+v %v", statusOf(resp), denied, err)
	}

	again := syncCharacter(t, owner, "Magni", "Everlook", "Dwarf", "Paladin", 13)
	if again.ID != ch.ID || again.Level != 13 || again.Backstory != "Left Ironforge with a hammer." {
		t.Fatalf("sync must keep backstory and update level: %+v", again)
	}
	if strings.TrimSpace(again.BattleTag) == "" {
		t.Fatal("synced character lost its account")
	}
}
