package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sagalore/sagalore/internal/backend"
	"github.com/sagalore/sagalore/internal/config"
)

func TestNeedsLoginAndRemind(t *testing.T) {
	var notes []string
	a := &App{
		Config: config.Config{Client: "forever"},
		Client: &backend.Client{},
		Log:    discardLog(),
		Notify: func(_, body string) { notes = append(notes, body) },
	}
	if !a.NeedsLogin() {
		t.Fatal("expected needs login")
	}
	a.maybeRemindLogin()
	if len(notes) != 0 {
		t.Fatalf("no remind before loginTried: %v", notes)
	}
	a.loginTried = true
	a.maybeRemindLogin()
	if len(notes) != 1 || notes[0] == "" {
		t.Fatalf("remind %+v", notes)
	}
	a.signedOut = true
	if a.NeedsLogin() {
		t.Fatal("logout should not need login nudge")
	}
	notes = nil
	a.maybeRemindLogin()
	if len(notes) != 0 {
		t.Fatalf("no remind after logout: %v", notes)
	}
}

func TestEnsureLoginTimeoutReminds(t *testing.T) {
	prev := loginTimeout
	loginTimeout = time.Millisecond
	t.Cleanup(func() { loginTimeout = prev })

	var notes []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v1/auth/bnet/start":
			w.Header().Set("Location", "https://example.invalid/oauth")
			w.WriteHeader(http.StatusFound)
		case strings.HasPrefix(r.URL.Path, "/v1/auth/bnet/poll"):
			w.WriteHeader(http.StatusAccepted)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	t.Setenv("SAGALORE_DEV_TOKEN", "")
	_ = os.Unsetenv("SAGALORE_DEV_TOKEN")
	a := &App{
		Config: config.Config{APIBaseURL: srv.URL, Client: "forever"},
		Client: &backend.Client{BaseURL: srv.URL, HTTP: srv.Client()},
		Log:    discardLog(),
		Notify: func(_, body string) { notes = append(notes, body) },
	}
	err := a.ensureLogin(context.Background())
	if !errors.Is(err, errLoginTimedOut) {
		t.Fatalf("err %v", err)
	}
	if !a.loginTried {
		t.Fatal("loginTried")
	}
	found := false
	for _, n := range notes {
		if strings.Contains(n, "timed out") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("timeout notice missing: %v", notes)
	}
}

func TestLoginUsesDevTokenBypass(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		if r.URL.Path != "/v1/auth/dev" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"token_type":"Bearer","session_token":"gss_test","expires_at":"2030-01-01T00:00:00Z","player":{"id":"p1","battletag":"Player#1234","display_name":"Player"}}`))
	}))
	defer srv.Close()
	t.Setenv("SAGALORE_DEV_TOKEN", "dev-secret")
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	a := &App{
		Config: config.Config{APIBaseURL: srv.URL, Client: "forever"},
		Client: &backend.Client{BaseURL: srv.URL, HTTP: srv.Client()},
		Log:    discardLog(),
	}
	if err := a.Login(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got != "Bearer dev-secret" {
		t.Fatalf("auth %q", got)
	}
	if a.Config.SessionToken != "gss_test" || a.Config.BattleTag != "Player#1234" {
		t.Fatalf("session %+v", a.Config)
	}
	if os.Getenv("SAGALORE_DEV_TOKEN") == "" {
		t.Fatal("dev token cleared")
	}
}
