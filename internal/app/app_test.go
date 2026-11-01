package app

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/sagalore/sagalore/internal/config"
	"github.com/sagalore/sagalore/internal/contract"
)

func TestHandleLineFlushPostsQuestVersion(t *testing.T) {
	var version, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/v1/sync":
			_, _ = w.Write([]byte(`{"battletag":"Magnus#2231","generated_at":"t","characters":[{"id":"char-1","name":"Magni","realm":"Argent Dawn","level":12}],"sagas":[{"id":"quest-1","title":"Første vakt","version":3,"active":true,"objectives":[]}],"progress":[],"completions":[]}`))
			return
		case "/v1/complete":
			_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil {
				t.Fatal(err)
			}
			mr := multipart.NewReader(r.Body, params["boundary"])
			for {
				p, err := mr.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				b, _ := io.ReadAll(p)
				if p.FormName() == "saga_version" {
					version = string(b)
				}
			}
			_, _ = w.Write([]byte(`{"allowed":true,"completed":true,"saga_version":3}`))
			return
		default:
			t.Fatalf("path %s", r.URL.Path)
		}
	}))
	defer srv.Close()

	a, err := New(config.Config{
		BattleTag: "Magnus#2231", APIBaseURL: srv.URL, APIKey: "gsk_test", Client: "forever",
	}, filepath.Join(t.TempDir(), "queue.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	a.NoUpdate = true
	a.sync = contract.SyncResponse{
		BattleTag: "Magnus#2231",
		Characters: []contract.Character{{
			ID: "char-1", Name: "Magni", Realm: "Argent Dawn", Level: 12,
		}},
		Quests: []contract.Quest{{
			ID: "quest-1", Title: "Første vakt", Version: 3, Active: true,
		}},
	}
	line := "[Sagalore] COMPLETE quest-1 3 Magni Argent Dawn 12 1761144240"
	if !a.HandleLine(line) {
		t.Fatal("enqueue")
	}
	if a.HandleLine(line) {
		t.Fatal("duplicate should be ignored")
	}
	a.Flush(context.Background())
	if version != "3" || auth != "Bearer gsk_test" {
		t.Fatalf("version %q auth %q", version, auth)
	}
	if a.Queue.Len() != 0 {
		t.Fatal("queue not drained")
	}
	// A later duplicate from the chat log stays deduped.
	if a.HandleLine(line) {
		t.Fatal("done key returned")
	}
	_ = time.Unix(1761144240, 0)
}
