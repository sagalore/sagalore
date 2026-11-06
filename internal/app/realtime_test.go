package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/sagalore/sagalore/internal/config"
	"github.com/sagalore/sagalore/internal/install"
	"github.com/sagalore/sagalore/internal/wowpath"
)

func TestPushRewritesLuaAndNotifies(t *testing.T) {
	var gen atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sync" {
			http.NotFound(w, r)
			return
		}
		body := `{"battletag":"Magnus#2231","generated_at":"t0","characters":[{"id":"c1","name":"Magni","realm":"Argent Dawn","level":20}],"sagas":[{"id":"q1","title":"Første vakt","version":1,"active":true,"party":true,"objectives":[]}],"progress":[]}`
		if gen.Load() > 0 {
			body = `{"battletag":"Magnus#2231","generated_at":"t1","characters":[{"id":"c1","name":"Magni","realm":"Argent Dawn","level":20}],"sagas":[{"id":"q1","title":"Første vakt","version":1,"active":true,"party":true,"objectives":[]},{"id":"q2","title":"En kveld i Goldshire","version":1,"active":true,"party":true,"objectives":[]}],"progress":[]}`
		}
		_, _ = w.Write([]byte(body))
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
	a.Flavors = []wowpath.Flavor{{Path: wow, Client: "forever"}}
	var notes []string
	a.Notify = func(title, body string) { notes = append(notes, title+": "+body) }

	if err := a.pullSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 0 {
		t.Fatalf("startup notified: %v", notes)
	}
	path := filepath.Join(wow, "Interface", "AddOns", "Sagalore", "SagaloreData.lua")
	first, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(first), "notice_id") {
		t.Fatal(string(first))
	}

	gen.Store(1)
	if err := a.pullSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 || notes[0] != "Sagalore: New saga: En kveld i Goldshire — type /reload in WoW" {
		t.Fatalf("%v", notes)
	}
	second, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(second)
	if !strings.Contains(text, `notice = "New saga: En kveld i Goldshire is loaded."`) || !strings.Contains(text, "En kveld i Goldshire") {
		t.Fatal(text)
	}

	if err := a.pullSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(notes) != 1 {
		t.Fatalf("repeat sync notified again: %v", notes)
	}
}

func TestPullSyncWritesAllFlavors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/sync" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"battletag":"Magnus#2231","generated_at":"t0","characters":[],"sagas":[{"id":"q1","title":"Første vakt","version":1,"active":true,"party":true,"objectives":[]}],"progress":[]}`))
	}))
	defer srv.Close()

	parent := t.TempDir()
	era := filepath.Join(parent, "_classic_era_")
	forever := filepath.Join(parent, "_forever_")
	for _, p := range []string{era, forever} {
		if err := os.MkdirAll(filepath.Join(p, "Interface", "AddOns"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := install.Install(forever, "forever"); err != nil {
		t.Fatal(err)
	}
	if err := install.Install(era, "classic_era"); err != nil {
		t.Fatal(err)
	}
	mainline, err := os.ReadFile(filepath.Join(forever, "Interface", "AddOns", "Sagalore", "Sagalore_Mainline.toc"))
	if err != nil || !strings.Contains(string(mainline), "Interface: 16001") {
		t.Fatalf("forever toc: %s %v", mainline, err)
	}
	eraTOC, _ := os.ReadFile(filepath.Join(era, "Interface", "AddOns", "Sagalore", "Sagalore.toc"))
	if !strings.Contains(string(eraTOC), "Interface: 11509") {
		t.Fatalf("era toc:\n%s", eraTOC)
	}

	a, err := New(config.Config{
		APIBaseURL: srv.URL, APIKey: "gss_test", Client: "classic_era",
	}, filepath.Join(t.TempDir(), "queue.json"), nil)
	if err != nil {
		t.Fatal(err)
	}
	a.NoUpdate = true
	a.Flavors = []wowpath.Flavor{
		{Path: forever, Client: "forever"},
		{Path: era, Client: "classic_era"},
	}

	if err := a.pullSync(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, f := range a.Flavors {
		path := filepath.Join(f.Path, "Interface", "AddOns", "Sagalore", "SagaloreData.lua")
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(body), "Første vakt") {
			t.Fatalf("%s: %s", f.Client, body)
		}
	}
}
