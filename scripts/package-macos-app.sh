#!/usr/bin/env bash
# Wrap a macOS companion binary as Sagalore.app and zip it for human downloads.
# Auto-update still uses the bare binary; this zip is for Finder double-click.
# Usage: package-macos-app.sh <binary-path> <version> <out-dir>
set -euo pipefail

if [[ $# -ne 3 ]]; then
  echo "usage: $0 <binary-path> <version> <out-dir>" >&2
  exit 2
fi

binary="$1"
version="${2#v}"
out_dir="$3"

if [[ ! -f "$binary" ]]; then
  echo "binary not found: $binary" >&2
  exit 1
fi
mkdir -p "$out_dir"
out_dir="$(cd "$out_dir" && pwd)"
binary="$(cd "$(dirname "$binary")" && pwd)/$(basename "$binary")"

asset_name="$(basename "$binary")"
zip_path="${out_dir}/${asset_name}.zip"

workdir="$(mktemp -d)"
cleanup() { rm -rf "$workdir"; }
trap cleanup EXIT

app="${workdir}/Sagalore.app"
macos="${app}/Contents/MacOS"
mkdir -p "$macos"
cp "$binary" "${macos}/Sagalore"
chmod +x "${macos}/Sagalore"

cat > "${app}/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleExecutable</key>
	<string>Sagalore</string>
	<key>CFBundleIdentifier</key>
	<string>app.sagalore.companion</string>
	<key>CFBundleInfoDictionaryVersion</key>
	<string>6.0</string>
	<key>CFBundleName</key>
	<string>Sagalore</string>
	<key>CFBundlePackageType</key>
	<string>APPL</string>
	<key>CFBundleShortVersionString</key>
	<string>${version}</string>
	<key>CFBundleVersion</key>
	<string>${version}</string>
	<key>LSMinimumSystemVersion</key>
	<string>12.0</string>
	<key>LSUIElement</key>
	<true/>
	<key>NSHighResolutionCapable</key>
	<true/>
</dict>
</plist>
EOF

rm -f "$zip_path"
(cd "$workdir" && zip -qry "$zip_path" Sagalore.app)
echo "wrote $zip_path"
