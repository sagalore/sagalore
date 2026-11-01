package update

import "testing"

func TestNewer(t *testing.T) {
	if Newer("v0.1.12", "v0.1.2") != true {
		t.Fatal("12 > 2")
	}
	if Newer("v0.1.2", "0.1.2") {
		t.Fatal("equal")
	}
	if Newer("v0.2.0", "v0.1.9") != true {
		t.Fatal("minor")
	}
	if Newer("v1.0.0", "0.1.0-dev") {
		t.Fatal("dev builds do not auto-update")
	}
	if AssetName("windows", "amd64") != "sagalore-windows-amd64.exe" {
		t.Fatal(AssetName("windows", "amd64"))
	}
	if AssetName("darwin", "arm64") != "sagalore-darwin-arm64" {
		t.Fatal(AssetName("darwin", "arm64"))
	}
}
