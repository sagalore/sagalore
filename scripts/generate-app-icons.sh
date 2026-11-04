#!/usr/bin/env bash
# Rebuild assets/AppIcon.icns, assets/app.ico, tray icon, and Windows .syso
# from assets/sagalore-emblem.png. Requires Python Pillow, iconutil, and Go.
set -euo pipefail
root="$(cd "$(dirname "$0")/.." && pwd)"
cd "$root"

python3 <<'PY'
from PIL import Image
from pathlib import Path
import struct, subprocess, shutil, io

root = Path('.').resolve()
src = root / 'assets' / 'sagalore-emblem.png'
tray = root / 'internal' / 'tray' / 'icon.png'
out_dir = root / 'assets'
work = Path('/tmp/sagalore-icons')
if work.exists():
    shutil.rmtree(work)
work.mkdir()

im = Image.open(src).convert('RGBA')
pixels = im.load()
w, h = im.size
for y in range(h):
    for x in range(w):
        r, g, b, a = pixels[x, y]
        if r < 18 and g < 18 and b < 18:
            pixels[x, y] = (0, 0, 0, 0)

def resize(size):
    return im.resize((size, size), Image.Resampling.LANCZOS)

resize(128).save(tray, 'PNG')

iconset = work / 'AppIcon.iconset'
iconset.mkdir()
for name, size in [
    ('icon_16x16.png', 16),
    ('icon_16x16@2x.png', 32),
    ('icon_32x32.png', 32),
    ('icon_32x32@2x.png', 64),
    ('icon_128x128.png', 128),
    ('icon_128x128@2x.png', 256),
    ('icon_256x256.png', 256),
    ('icon_256x256@2x.png', 512),
    ('icon_512x512.png', 512),
    ('icon_512x512@2x.png', 1024),
]:
    resize(size).save(iconset / name, 'PNG')

icns = out_dir / 'AppIcon.icns'
subprocess.check_call(['iconutil', '-c', 'icns', str(iconset), '-o', str(icns)])

images = []
for s in [16, 24, 32, 48, 64, 128, 256]:
    buf = io.BytesIO()
    resize(s).save(buf, format='PNG')
    images.append((s, buf.getvalue()))

ico_path = out_dir / 'app.ico'
with open(ico_path, 'wb') as f:
    count = len(images)
    f.write(struct.pack('<HHH', 0, 1, count))
    offset = 6 + count * 16
    entries = []
    for s, data in images:
        dim = 0 if s >= 256 else s
        entries.append((dim, dim, len(data), offset))
        offset += len(data)
    for w, h, size, off in entries:
        f.write(struct.pack('<BBBBHHII', w, h, 0, 0, 1, 32, size, off))
    for _, data in images:
        f.write(data)

print('wrote', tray)
print('wrote', icns)
print('wrote', ico_path)
PY

go run github.com/akavel/rsrc@v0.10.2 -arch amd64 -ico assets/app.ico -o cmd/sagalore/rsrc_windows_amd64.syso
echo "wrote cmd/sagalore/rsrc_windows_amd64.syso"
