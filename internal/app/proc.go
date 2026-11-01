package app

import (
	"archive/zip"
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/sagalore/sagalore/internal/wowpath"
)

func osName() string   { return runtime.GOOS }
func archName() string { return runtime.GOARCH }

func restart() {
	exe, err := os.Executable()
	if err != nil {
		os.Exit(0)
	}
	cmd := exec.Command(exe, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	_ = cmd.Start()
	os.Exit(0)
}

func installZip(wowDir string, body []byte) error {
	r, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		return err
	}
	dest := wowpath.AddonDir(wowDir)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, f := range r.File {
		rel, ok := addonZipRel(f.Name)
		if !ok || f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		data, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return err
		}
		target := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// addonZipRel is the path inside Interface/AddOns/Sagalore. The release zip
// is rooted at Sagalore/. Generated data files are
// skipped so an update does not wipe the synced saga list.
func addonZipRel(name string) (string, bool) {
	name = filepath.ToSlash(name)
	name = strings.TrimPrefix(name, "./")
	for _, prefix := range []string{"Sagalore/"} {
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
			break
		}
	}
	if name == "" || strings.HasSuffix(name, "/") {
		return "", false
	}
	base := filepath.Base(name)
	if base == "SagaloreData.lua" {
		return "", false
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == "." || clean == "" || strings.HasPrefix(clean, "..") {
		return "", false
	}
	return clean, true
}
