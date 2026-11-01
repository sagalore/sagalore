package install

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sagalore/sagalore/addon"
	"github.com/sagalore/sagalore/internal/envcompat"
	"github.com/sagalore/sagalore/internal/wowpath"
)

// Install copies the addon into Interface/AddOns/Sagalore.
// When SAGALORE_ADDON_SRC is set to a Sagalore folder (or its parent that
// contains Sagalore/), that tree is used instead of the embedded copy — so
// local go run picks up lua edits without rebuilding the binary.
// Each flavor TOC keeps its own interface number. Sagalore.toc is the fallback
// and matches mode. mode "forever" rewrites every TOC, because that client may
// load the Mainline file. An existing SagaloreData.lua is kept.
func Install(wowDir, mode string) error {
	dest := wowpath.AddonDir(wowDir)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	mode = wowpath.NormalizeClient(mode)
	if src := addonSrc(); src != "" {
		return installFromDir(src, dest, mode)
	}
	return installEmbedded(dest, mode)
}

func addonSrc() string {
	src := envcompat.Get("SAGALORE_ADDON_SRC")
	if src == "" {
		return ""
	}
	if st, err := os.Stat(filepath.Join(src, "Sagalore.lua")); err == nil && !st.IsDir() {
		return src
	}
	nested := filepath.Join(src, "Sagalore")
	if st, err := os.Stat(filepath.Join(nested, "Sagalore.lua")); err == nil && !st.IsDir() {
		return nested
	}
	return ""
}

func installEmbedded(dest, mode string) error {
	bodies, err := readTOCBodies(func(name string) ([]byte, error) {
		return addon.Files.ReadFile("Sagalore/" + name)
	})
	if err != nil {
		return err
	}
	return fs.WalkDir(addon.Files, "Sagalore", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel := strings.TrimPrefix(path, "Sagalore")
		rel = strings.TrimPrefix(rel, "/")
		if rel == "" {
			return nil
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if filepath.Base(rel) == "SagaloreData.lua" {
			if _, err := os.Stat(target); err == nil {
				return nil
			}
		}
		body, err := addon.Files.ReadFile(path)
		if err != nil {
			return err
		}
		if isTOC(rel) {
			body = tocBody(mode, filepath.Base(rel), bodies, body)
		}
		return writeFile(target, body)
	})
}

func installFromDir(src, dest, mode string) error {
	bodies, err := readTOCBodies(func(name string) ([]byte, error) {
		return os.ReadFile(filepath.Join(src, name))
	})
	if err != nil {
		return err
	}
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		target := filepath.Join(dest, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if d.Name() == "SagaloreData.lua" {
			if _, err := os.Stat(target); err == nil {
				return nil
			}
		}
		body, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if isTOC(d.Name()) {
			body = tocBody(mode, d.Name(), bodies, body)
		}
		return writeFile(target, body)
	})
}

func readTOCBodies(read func(string) ([]byte, error)) (map[string][]byte, error) {
	bodies := map[string][]byte{}
	for _, name := range []string{
		"Sagalore_Forever.toc",
		"Sagalore_Mainline.toc",
		"Sagalore_Vanilla.toc",
		"Sagalore_Mists.toc",
		"Sagalore_TBC.toc",
	} {
		body, err := read(name)
		if err != nil {
			return nil, err
		}
		bodies[name] = body
	}
	return bodies, nil
}

func isTOC(name string) bool {
	return len(name) > 4 && name[len(name)-4:] == ".toc"
}

func tocBody(mode, name string, bodies map[string][]byte, original []byte) []byte {
	forever := bodies["Sagalore_Forever.toc"]
	if mode == "forever" {
		return forever
	}
	if name != "Sagalore.toc" {
		if body, ok := bodies[name]; ok {
			return body
		}
		return original
	}
	switch mode {
	case "retail":
		return bodies["Sagalore_Mainline.toc"]
	case "classic_era":
		return bodies["Sagalore_Vanilla.toc"]
	case "classic":
		return bodies["Sagalore_Mists.toc"]
	case "anniversary":
		return bodies["Sagalore_TBC.toc"]
	default:
		return forever
	}
}

// WriteLanguage records the companion language for the addon.
// English only for now; lang is ignored so a stale config cannot flip the UI.
func WriteLanguage(wowDir, lang string) error {
	_ = lang
	path := filepath.Join(wowpath.AddonDir(wowDir), "SagaloreLanguage.lua")
	body := []byte("-- English only for now; other catalogs remain in Locales/ for later.\nSagaloreLanguage = \"en\"\n")
	return writeFile(path, body)
}

func writeFile(path string, body []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(path)
	}
	return os.Rename(tmp, path)
}
