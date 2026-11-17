package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sagalore/sagalore/internal/config"
	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/wowpath"
)

func TestPullSyncUpgradeRequiredAnnounces(t *testing.T) {
	var sawProto string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawProto = r.Header.Get(contract.ProtocolHeader)
		w.WriteHeader(http.StatusUpgradeRequired)
		_, _ = w.Write([]byte(`{"error":"upgrade_required","message":"Update Sagalore. This companion speaks protocol 0; the server requires 1–2. Install the update, then type /reload in game."}`))
	}))
	defer srv.Close()

	a, err := New(config.Config{
		APIBaseURL: srv.URL, APIKey: "gss_test", Client: "retail",
	}, filepath.Join(t.TempDir(), "queue.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	a.NoUpdate = true
	var notes []string
	a.Notify = func(_, body string) { notes = append(notes, body) }
	err = a.pullSync(context.Background())
	if err == nil {
		t.Fatal("expected upgrade error")
	}
	if sawProto != "2" {
		t.Fatalf("protocol header %q", sawProto)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "Update Sagalore") || !strings.Contains(notes[0], "/reload") {
		t.Fatalf("notes %v err %v", notes, err)
	}
}

func TestPullSyncWritesServerProtocol(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"battletag":"Magnus#2231","generated_at":"t","protocol":{"version":2,"min":1,"max":2},"characters":[],"sagas":[],"progress":[]}`))
	}))
	defer srv.Close()
	a, err := New(config.Config{
		APIBaseURL: srv.URL, APIKey: "gss_test", Client: "retail",
	}, filepath.Join(t.TempDir(), "queue.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	a.NoUpdate = true
	wow := t.TempDir()
	a.Flavors = []wowpath.Flavor{{Path: wow, Client: "retail"}}
	if err := a.pullSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(wow, "Interface", "AddOns", "Sagalore", "SagaloreData.lua"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "protocol = 2") {
		t.Fatal(string(raw))
	}
}
