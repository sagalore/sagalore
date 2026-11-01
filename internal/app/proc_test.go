package app

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestAddonZipKeepsLocalesAndSkipsData(t *testing.T) {
	if rel, ok := addonZipRel("Sagalore/Locales/enUS.lua"); !ok || rel != filepath.Join("Locales", "enUS.lua") {
		t.Fatalf("rel %q ok %v", rel, ok)
	}
	if _, ok := addonZipRel("Sagalore/SagaloreData.lua"); ok {
		t.Fatal("data file should be skipped")
	}
	if _, ok := addonZipRel("../Sagalore.lua"); ok {
		t.Fatal("zip slip")
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	write := func(name, body string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	write("Sagalore/Locales/enUS.lua", "locale")
	write("Sagalore/Sagalore.lua", "addon")
	write("Sagalore/SagaloreData.lua", "do not copy")
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	wow := t.TempDir()
	if err := installZip(wow, buf.Bytes()); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(wow, "Interface", "AddOns", "Sagalore")
	if b, err := os.ReadFile(filepath.Join(root, "Locales", "enUS.lua")); err != nil || string(b) != "locale" {
		t.Fatalf("locale %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(root, "SagaloreData.lua")); !os.IsNotExist(err) {
		t.Fatal("data file was written from the zip")
	}
}
