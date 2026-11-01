package wowpath

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultCandidatesWindowsAndMac(t *testing.T) {
	env := map[string]string{
		"ProgramFiles(x86)": `C:\PF86`,
		"ProgramFiles":      `C:\PF`,
		"LOCALAPPDATA":      `C:\Users\A\AppData\Local`,
		"HOME":              `/Users/a`,
	}
	getenv := func(k string) string { return env[k] }
	win := DefaultCandidates("windows", getenv)
	if !contains(win, filepath.Join(`C:\PF86`, "World of Warcraft Forever")) {
		t.Fatalf("windows candidates %#v", win)
	}
	if !contains(win, filepath.Join(`C:\PF`, "World of Warcraft")) {
		t.Fatalf("windows retail parent missing %#v", win)
	}
	mac := DefaultCandidates("darwin", getenv)
	if !contains(mac, "/Applications/World of Warcraft Forever") {
		t.Fatalf("mac %#v", mac)
	}
	if !contains(mac, filepath.Join("/Users/a", "Applications", "World of Warcraft")) {
		t.Fatalf("home mac %#v", mac)
	}
}

func TestDetectPrefersForever(t *testing.T) {
	root := t.TempDir()
	retail := filepath.Join(root, "World of Warcraft", "_retail_")
	forever := filepath.Join(root, "World of Warcraft Forever")
	for _, p := range []string{retail, forever} {
		if err := os.MkdirAll(filepath.Join(p, "Interface", "AddOns"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Detect("", []string{
		filepath.Join(root, "World of Warcraft"),
		forever,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got != forever {
		t.Fatalf("got %s want forever %s", got, forever)
	}
	parent := filepath.Join(root, "World of Warcraft")
	resolved, ok := Resolve(parent)
	if !ok || resolved != retail {
		t.Fatalf("resolve parent %s ok=%v", resolved, ok)
	}
	configured, err := Detect(forever, nil)
	if err != nil || configured != forever {
		t.Fatalf("configured %s %v", configured, err)
	}
	if _, err := Detect(filepath.Join(root, "missing"), nil); err == nil {
		t.Fatal("expected error for bad wow_path")
	}
}

func TestDetectClientPrefersRetail(t *testing.T) {
	root := t.TempDir()
	retail := filepath.Join(root, "World of Warcraft", "_retail_")
	forever := filepath.Join(root, "World of Warcraft Forever")
	for _, p := range []string{retail, forever} {
		if err := os.MkdirAll(filepath.Join(p, "Interface", "AddOns"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DetectClient("", []string{
		filepath.Join(root, "World of Warcraft"),
		forever,
	}, "retail")
	if err != nil || got != retail {
		t.Fatalf("got %s err %v", got, err)
	}
}

func TestDetectClassicFlavors(t *testing.T) {
	root := t.TempDir()
	era := filepath.Join(root, "World of Warcraft", "_classic_era_")
	classic := filepath.Join(root, "World of Warcraft", "_classic_")
	for _, p := range []string{era, classic} {
		if err := os.MkdirAll(filepath.Join(p, "Interface", "AddOns"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	parent := filepath.Join(root, "World of Warcraft")
	got, err := DetectClient(parent, nil, "classic_era")
	if err != nil || got != era {
		t.Fatalf("era got %s err %v", got, err)
	}
	got, err = DetectClient(parent, nil, "classic")
	if err != nil || got != classic {
		t.Fatalf("classic got %s err %v", got, err)
	}
	only := t.TempDir()
	justClassic := filepath.Join(only, "World of Warcraft", "_classic_")
	if err := os.MkdirAll(filepath.Join(justClassic, "Interface", "AddOns"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err = DetectClient("", []string{filepath.Join(only, "World of Warcraft")}, "forever")
	if err != nil || got != justClassic {
		t.Fatalf("auto got %s err %v", got, err)
	}
	if NormalizeClient("vanilla") != "classic_era" || NormalizeClient("mists") != "classic" {
		t.Fatal("aliases")
	}
}

func TestAddonPaths(t *testing.T) {
	base := filepath.Join("Wow", "forever")
	if !strings.HasSuffix(AddonDir(base), filepath.Join("Interface", "AddOns", "Sagalore")) {
		t.Fatal(AddonDir(base))
	}
	if filepath.Base(ChatLog(base)) != "WoWChatLog.txt" {
		t.Fatal(ChatLog(base))
	}
	if runtime.GOOS == "windows" && !strings.Contains(ChatLog(base), `\`) && !strings.Contains(ChatLog(base), "/") {
		t.Fatal(ChatLog(base))
	}
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
