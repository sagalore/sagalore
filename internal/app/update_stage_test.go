package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/sagalore/sagalore/internal/config"
	"github.com/sagalore/sagalore/internal/update"
	"github.com/sagalore/sagalore/internal/version"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestOfferUpdateStagesAndNotifiesOnce(t *testing.T) {
	name := update.AssetName(runtime.GOOS, runtime.GOARCH)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/sagalore/sagalore/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":%q,"url":%q},{"name":%q,"url":%q}]}`,
				name, srv.URL+"/bin", update.AddonZip, srv.URL+"/zip")
		case r.URL.Path == "/bin":
			_, _ = w.Write([]byte("new-binary"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	var notes int
	a := &App{
		Config:    config.Config{Update: config.Update{Repo: config.DefaultUpdateRepo}},
		Log:       discardLog(),
		UpdateDir: dir,
		OnUpdateAvailable: func(tag string) {
			notes++
			if tag != "v9.9.9" {
				t.Errorf("tag %s", tag)
			}
		},
	}
	client := &update.Client{APIBase: srv.URL}
	a.offerUpdate(context.Background(), client, config.DefaultUpdateRepo, "0.1.0")
	if notes != 1 || a.PendingUpdate() != "v9.9.9" {
		t.Fatalf("notes %d pending %s", notes, a.PendingUpdate())
	}
	body, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil || string(body) != "new-binary" {
		t.Fatalf("staged %v %q", err, body)
	}
	a.offerUpdate(context.Background(), client, config.DefaultUpdateRepo, "0.1.0")
	if notes != 1 {
		t.Fatalf("notified again: %d", notes)
	}
}

func TestMaybeUpdateSkipsWhenDisabled(t *testing.T) {
	a := &App{Config: config.Config{}, Log: discardLog(), UpdateDir: t.TempDir()}
	a.Config.Update.SetEnabled(false)
	a.maybeUpdate(context.Background())
	if a.PendingUpdate() != "" {
		t.Fatal(a.PendingUpdate())
	}
}

func TestCheckUpdateIgnoresDisabledPreference(t *testing.T) {
	name := update.AssetName(runtime.GOOS, runtime.GOARCH)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/sagalore/sagalore/releases/latest":
			fmt.Fprintf(w, `{"tag_name":"v9.9.9","assets":[{"name":%q,"url":%q}]}`, name, srv.URL+"/bin")
		case r.URL.Path == "/bin":
			_, _ = w.Write([]byte("new-binary"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	prev := version.Version
	version.Version = "0.1.0"
	t.Cleanup(func() { version.Version = prev })

	a := &App{
		Config:       config.Config{Update: config.Update{Repo: config.DefaultUpdateRepo}},
		Log:          discardLog(),
		UpdateDir:    t.TempDir(),
		UpdateClient: &update.Client{APIBase: srv.URL},
	}
	a.Config.Update.SetEnabled(false)
	a.maybeUpdate(context.Background())
	if a.PendingUpdate() != "" {
		t.Fatal("automatic path should stay quiet when disabled")
	}
	tag, ready := a.CheckUpdate(context.Background())
	if !ready || tag != "v9.9.9" {
		t.Fatalf("ready=%v tag=%q", ready, tag)
	}
}
