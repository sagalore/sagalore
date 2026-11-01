package screenshot

import (
	"os"
	"path/filepath"
	"strings"
	"time"
)

var imageExt = map[string]bool{
	".png": true, ".jpg": true, ".jpeg": true, ".tga": true, ".bmp": true, ".webp": true,
}

// Closest returns the image in dir whose modification time is nearest to around,
// if one falls inside slack. A zero around picks the newest image.
func Closest(dir string, around time.Time, slack time.Duration) (string, bool) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	bestPath := ""
	bestScore := time.Duration(1<<63 - 1)
	var bestMod time.Time
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !imageExt[strings.ToLower(filepath.Ext(e.Name()))] {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		mod := info.ModTime()
		if around.IsZero() {
			if bestPath == "" || mod.After(bestMod) {
				bestPath = filepath.Join(dir, e.Name())
				bestMod = mod
			}
			continue
		}
		delta := mod.Sub(around)
		if delta < 0 {
			delta = -delta
		}
		if delta <= slack && delta < bestScore {
			bestScore = delta
			bestPath = filepath.Join(dir, e.Name())
			bestMod = mod
		}
	}
	return bestPath, bestPath != ""
}
