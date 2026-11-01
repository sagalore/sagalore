package backend

import (
	"context"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestCompleteSendsVersionAndOmitsEmptyScreenshot(t *testing.T) {
	var gotAuth, gotVersion, gotFile string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.URL.Path == "/v1/sync" {
			_, _ = w.Write([]byte(`{"battletag":"Magnus#2231","characters":[],"sagas":[],"progress":[]}`))
			return
		}
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
				gotVersion = string(b)
			}
			if p.FormName() == "screenshot" {
				gotFile = string(b)
			}
		}
		_, _ = w.Write([]byte(`{"allowed":true,"completed":true,"saga_version":3}`))
	}))
	defer srv.Close()
	c := &Client{BaseURL: srv.URL + "/", APIKey: "gsk_test", HTTP: srv.Client()}
	res, err := c.Complete(context.Background(), contract.CompletionRequest{
		CharacterID: "c", QuestID: "q", QuestVersion: 3, BattleTag: "Magnus#2231", Level: 12,
		CompletedAt: "2026-10-22T14:44:00Z",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer gsk_test" || gotVersion != "3" || gotFile != "" {
		t.Fatalf("auth %q version %q file %q", gotAuth, gotVersion, gotFile)
	}
	if !res.Completed || res.QuestVersion != 3 {
		t.Fatalf("%+v", res)
	}
	if _, err := c.Sync(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = w.Write([]byte(`{"error":"forbidden","message":"nei"}`))
	}))
	defer srv2.Close()
	c.BaseURL = srv2.URL
	_, err = c.Level(context.Background(), contract.LevelRequest{CharacterID: "c", BattleTag: "Magnus#2231", Level: 2})
	httpErr, ok := err.(*HTTPError)
	if !ok || !httpErr.Permanent() || httpErr.Status != 403 {
		t.Fatalf("%v", err)
	}
}
