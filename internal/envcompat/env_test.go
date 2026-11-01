package envcompat

import "testing"

func TestGet(t *testing.T) {
	t.Setenv("SAGALORE_DEV_TOKEN", "token")
	if got := Get("SAGALORE_DEV_TOKEN"); got != "token" {
		t.Fatalf("got %q", got)
	}
	t.Setenv("SAGALORE_DEV_TOKEN", "  ")
	if got := Get("SAGALORE_DEV_TOKEN"); got != "" {
		t.Fatalf("blank, got %q", got)
	}
	t.Setenv("DATABASE_URL", "postgres://local")
	if got := Get("DATABASE_URL"); got != "postgres://local" {
		t.Fatalf("unrelated %q", got)
	}
}
