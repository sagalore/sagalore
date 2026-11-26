package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestAuthorPatchFillsChangesAndFetchesCover(t *testing.T) {
	resetDB(t)
	png := tinyPNG(t)
	var fetched string
	srv := httptest.NewServer((&Server{
		store: NewStore(testPool),
		fetchCoverFn: func(_ context.Context, raw string) ([]byte, string, error) {
			fetched = raw
			if raw != "https://cdn.example/cover.png" {
				return nil, "", errCoverURL
			}
			return png, "image/png", nil
		},
	}).Handler())
	t.Cleanup(srv.Close)
	player, token := devSession(t, "bnet-author", "Author#1", "Author")
	devGroup(t, player.ID, "friends", "Friends")
	author := apiClient{base: srv.URL, token: token, http: srv.Client()}

	var created contract.Quest
	resp, err := author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Bread run", "description": "Hold a loaf.", "party": true,
		"image_url": "https://cdn.example/cover.png",
		"objectives": []map[string]any{{
			"id": "loaf", "type": "item", "text": "Have bread", "item_id": 4540, "show_count": false,
		}},
	}, &created)
	if err != nil || resp.StatusCode != http.StatusCreated || !created.HasImage || created.Version != 1 {
		t.Fatalf("create %d %v %+v", statusOf(resp), err, created)
	}
	if fetched != "https://cdn.example/cover.png" {
		t.Fatalf("fetched %q", fetched)
	}
	if len(created.Objectives) != 1 || created.Objectives[0].Count != 1 || created.Objectives[0].ShowCount == nil || *created.Objectives[0].ShowCount {
		t.Fatalf("objective %+v", created.Objectives)
	}
	if created.Objectives[0].Memory != contract.MemoryHold {
		t.Fatalf("memory %s", created.Objectives[0].Memory)
	}

	resp, err = author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Blocked", "description": "No.", "party": true,
		"image_url":  "http://127.0.0.1/a.png",
		"objectives": []map[string]any{{"id": "x", "type": "talk", "text": "Talk", "npc": "Farley"}},
	}, nil)
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad image_url %d %v", statusOf(resp), err)
	}

	var patched contract.Quest
	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+created.ID, map[string]any{
		"title": "Bread run, again",
	}, &patched)
	if err != nil || resp.StatusCode != http.StatusOK || patched.Title != "Bread run, again" || patched.Version != 2 {
		t.Fatalf("patch %d %v %+v", statusOf(resp), err, patched)
	}

	var kept contract.Quest
	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+created.ID, map[string]any{
		"description": "Still a loaf.", "changes": []string{"Clearer description"},
	}, &kept)
	if err != nil || resp.StatusCode != http.StatusOK || kept.Version != 3 {
		t.Fatalf("explicit changes %d %v %+v", statusOf(resp), err, kept)
	}

	var retired contract.Quest
	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+created.ID, map[string]any{
		"active": false,
	}, &retired)
	if err != nil || resp.StatusCode != http.StatusOK || retired.Active || retired.Version != 4 {
		t.Fatalf("retire %d %v %+v", statusOf(resp), err, retired)
	}

	before := retired.Version
	var cover contract.Quest
	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+created.ID, map[string]any{
		"image_url": "https://cdn.example/cover.png",
	}, &cover)
	if err != nil || resp.StatusCode != http.StatusOK || !cover.HasImage || cover.Version != before {
		t.Fatalf("cover-only %d %v version %d", statusOf(resp), err, cover.Version)
	}

	var detail contract.QuestDetail
	resp, err = author.do(http.MethodGet, "/v1/sagas/"+created.ID, nil, &detail)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("get %d %v", statusOf(resp), err)
	}
	if len(detail.Versions) != 4 {
		t.Fatalf("versions %d", len(detail.Versions))
	}
	if !containsNote(detail.Versions[1].Changes, "Updated title") {
		t.Fatalf("title notes %v", detail.Versions[1].Changes)
	}
	if !containsNote(detail.Versions[2].Changes, "Clearer description") || containsNote(detail.Versions[2].Changes, "Updated description") {
		t.Fatalf("author notes %v", detail.Versions[2].Changes)
	}
	if !containsNote(detail.Versions[3].Changes, "Retired saga") {
		t.Fatalf("retire notes %v", detail.Versions[3].Changes)
	}
	if detail.Objectives[0].ShowCount == nil || *detail.Objectives[0].ShowCount || detail.Objectives[0].Count != 1 {
		t.Fatalf("stored objective %+v", detail.Objectives[0])
	}
}

func containsNote(notes []string, want string) bool {
	for _, note := range notes {
		if note == want {
			return true
		}
	}
	return false
}
