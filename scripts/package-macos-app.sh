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
repo_root="$(cd "$(dirname "$0")/.." && pwd)"
icns="${repo_root}/assets/AppIcon.icns"
if [[ ! -f "$icns" ]]; then
  echo "missing $icns — run scripts/generate-app-icons.sh" >&2
  exit 1
fi

workdir="$(mktemp -d)"
cleanup() { rm -rf "$workdir"; }
trap cleanup EXIT

stage="${workdir}/stage"
app="${stage}/Sagalore.app"
macos="${app}/Contents/MacOS"
resources="${app}/Contents/Resources"
mkdir -p "$macos" "$resources"
cp "$binary" "${macos}/Sagalore"
chmod +x "${macos}/Sagalore"
cp "$icns" "${resources}/AppIcon.icns"

cat > "${app}/Contents/Info.plist" <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>CFBundleDevelopmentRegion</key>
	<string>en</string>
	<key>CFBundleExecutable</key>
	<string>Sagalore</string>
	<key>CFBundleIconFile</key>
	<string>AppIcon</string>
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

# Go's linker ad-hoc signature does not bind Info.plist; Gatekeeper then says
# the app is "damaged". Re-sign the whole bundle before archiving.
codesign --force --deep --sign - "$app"
codesign --verify --deep --strict "$app"

# One-click helper if the download is still quarantined after unzip.
cat > "${stage}/Open Sagalore.command" <<'EOF'
#!/bin/bash
cd "$(dirname "$0")"
xattr -cr "Sagalore.app" >/dev/null 2>&1 || true
open "Sagalore.app"
EOF
chmod +x "${stage}/Open Sagalore.command"

# ditto --norsrc keeps the codesign seal intact through a normal unzip
# (plain zip / AppleDouble resource forks break sealed resources).
rm -f "$zip_path"
(cd "$stage" && ditto -c -k --norsrc . "$zip_path")
echo "wrote $zip_path"
