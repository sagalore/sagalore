// Package update checks GitHub releases and replaces the companion binary.
package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

type Asset struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	URL  string `json:"url"`
}

type Release struct {
	Tag    string  `json:"tag_name"`
	Assets []Asset `json:"assets"`
}

func AssetName(goos, goarch string) string {
	name := "sagalore-" + goos + "-" + goarch
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

const AddonZip = "Sagalore-addon.zip"

// Newer reports whether latest should replace current.
// Development builds (version containing "dev") never auto-update.
func Newer(latest, current string) bool {
	if strings.Contains(current, "dev") || latest == "" || current == "" {
		return false
	}
	lv := parse(latest)
	cv := parse(current)
	for i := 0; i < 3; i++ {
		if lv[i] != cv[i] {
			return lv[i] > cv[i]
		}
	}
	return false
}

func parse(v string) [3]int {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexAny(v, "-+"); i >= 0 {
		v = v[:i]
	}
	var out [3]int
	parts := strings.Split(v, ".")
	for i := 0; i < len(parts) && i < 3; i++ {
		n, _ := strconv.Atoi(parts[i])
		out[i] = n
	}
	return out
}

type Client struct {
	HTTP    *http.Client
	Token   string
	APIBase string // default https://api.github.com; tests set this
}

func (c *Client) apiBase() string {
	if strings.TrimSpace(c.APIBase) != "" {
		return strings.TrimRight(c.APIBase, "/")
	}
	return "https://api.github.com"
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (c *Client) Latest(ctx context.Context, repo string) (Release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.apiBase()+"/repos/"+repo+"/releases/latest", nil)
	if err != nil {
		return Release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "Sagalore")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return Release{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return Release{}, fmt.Errorf("github status %d", resp.StatusCode)
	}
	var rel Release
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return Release{}, err
	}
	return rel, nil
}

func (c *Client) Download(ctx context.Context, asset Asset) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, asset.URL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("User-Agent", "Sagalore")
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("asset status %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, 200<<20))
}

// ReplaceExecutable swaps the running binary for newBytes.
// On Windows the running exe is renamed away first.
func ReplaceExecutable(newBytes []byte) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	exe, err = filepath.EvalSymlinks(exe)
	if err != nil {
		return err
	}
	tmp := exe + ".new"
	if err := os.WriteFile(tmp, newBytes, 0o755); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		old := exe + ".old"
		_ = os.Remove(old)
		if err := os.Rename(exe, old); err != nil {
			return err
		}
		if err := os.Rename(tmp, exe); err != nil {
			_ = os.Rename(old, exe)
			return err
		}
		return nil
	}
	if err := os.Rename(tmp, exe); err != nil {
		return err
	}
	return nil
}

func FindAsset(rel Release, name string) (Asset, bool) {
	for _, a := range rel.Assets {
		if a.Name == name {
			return a, true
		}
	}
	return Asset{}, false
}
