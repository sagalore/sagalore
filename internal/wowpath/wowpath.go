// Package wowpath finds a World of Warcraft flavor directory.
package wowpath

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// DefaultCandidates lists install locations for a GOOS. env is os.Getenv when nil.
func DefaultCandidates(goos string, env func(string) string) []string {
	if env == nil {
		env = os.Getenv
	}
	home := env("HOME")
	if home == "" {
		home = env("USERPROFILE")
	}
	switch goos {
	case "windows":
		pf86 := env("ProgramFiles(x86)")
		pf := env("ProgramFiles")
		local := env("LOCALAPPDATA")
		var roots []string
		for _, r := range []string{pf86, pf, local} {
			if r == "" {
				continue
			}
			roots = append(roots,
				filepath.Join(r, "World of Warcraft Forever"),
				filepath.Join(r, "World of Warcraft"),
			)
		}
		return roots
	default:
		var roots []string
		if home != "" {
			roots = append(roots,
				filepath.Join(home, "Applications", "World of Warcraft Forever"),
				filepath.Join(home, "Applications", "World of Warcraft"),
			)
		}
		roots = append(roots,
			"/Applications/World of Warcraft Forever",
			"/Applications/World of Warcraft",
		)
		return roots
	}
}

func Candidates() []string {
	return DefaultCandidates(runtime.GOOS, os.Getenv)
}

func isFlavor(path string) bool {
	info, err := os.Stat(filepath.Join(path, "Interface", "AddOns"))
	return err == nil && info.IsDir()
}

// NormalizeClient accepts the flags and folder nicknames the companion understands.
func NormalizeClient(client string) string {
	switch strings.ToLower(strings.TrimSpace(client)) {
	case "", "forever":
		return "forever"
	case "retail", "mainline":
		return "retail"
	case "classic_era", "classic-era", "classicera", "era", "vanilla":
		return "classic_era"
	case "classic", "mists", "progression":
		return "classic"
	case "anniversary", "tbc":
		return "anniversary"
	default:
		return strings.ToLower(strings.TrimSpace(client))
	}
}

// ValidClient reports whether client is a known flavor after normalization.
func ValidClient(client string) bool {
	switch NormalizeClient(client) {
	case "forever", "retail", "classic_era", "classic", "anniversary":
		return true
	default:
		return false
	}
}

func flavorKind(path string) string {
	// Only the install folder name counts. A home directory named "forever"
	// must not beat an actual client folder. "_classic_era_" is not "_classic_".
	base := strings.ToLower(filepath.Base(path))
	switch {
	case strings.Contains(base, "classic_era") || strings.Contains(base, "_classic_era_"):
		return "classic_era"
	case strings.Contains(base, "anniversary"):
		return "anniversary"
	case base == "_classic_" || base == "classic":
		return "classic"
	case strings.Contains(base, "_retail_") || strings.HasSuffix(base, "retail"):
		return "retail"
	case strings.Contains(base, "forever"):
		return "forever"
	default:
		return ""
	}
}

func score(path, client string) int {
	kind := flavorKind(path)
	client = NormalizeClient(client)
	if kind != "" && kind == client {
		return 100
	}
	switch client {
	case "forever":
		if kind == "retail" {
			return 20
		}
	case "classic_era":
		if kind == "anniversary" {
			return 40
		}
		if kind == "classic" {
			return 30
		}
	case "classic":
		if kind == "classic_era" {
			return 30
		}
	case "anniversary":
		if kind == "classic_era" {
			return 30
		}
	case "retail":
		if kind == "forever" {
			return 10
		}
	}
	if kind != "" {
		return 5
	}
	return 1
}

func flavorOrder(client string) []string {
	switch NormalizeClient(client) {
	case "retail":
		return []string{"_retail_", "_forever_", "forever", "_classic_era_", "_classic_", "_anniversary_"}
	case "classic_era":
		return []string{"_classic_era_", "_anniversary_", "_classic_", "_retail_", "_forever_", "forever"}
	case "classic":
		return []string{"_classic_", "_classic_era_", "_anniversary_", "_retail_", "_forever_", "forever"}
	case "anniversary":
		return []string{"_anniversary_", "_classic_era_", "_classic_", "_retail_", "_forever_", "forever"}
	default:
		return []string{"_forever_", "forever", "_retail_", "_classic_era_", "_classic_", "_anniversary_"}
	}
}

// Resolve returns the flavor directory (the one that contains Interface/AddOns).
// A parent install folder is accepted; Forever is preferred over retail.
func Resolve(path string) (string, bool) {
	return resolve(path, "forever")
}

// ResolveClient is Resolve with an explicit client, "forever" or "retail".
func ResolveClient(path, client string) (string, bool) {
	return resolve(path, client)
}

func resolve(path, client string) (string, bool) {
	path = filepath.Clean(path)
	if isFlavor(path) {
		return path, true
	}
	info, err := os.Stat(path)
	if err != nil || !info.IsDir() {
		return "", false
	}
	for _, name := range flavorOrder(client) {
		candidate := filepath.Join(path, name)
		if isFlavor(candidate) {
			return candidate, true
		}
	}
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", false
	}
	var found []string
	for _, e := range entries {
		candidate := filepath.Join(path, e.Name())
		if isFlavor(candidate) {
			found = append(found, candidate)
		}
	}
	if len(found) == 0 {
		return "", false
	}
	sort.Slice(found, func(i, j int) bool { return score(found[i], client) > score(found[j], client) })
	return found[0], true
}

// Detect uses configured when it points at a WoW directory, otherwise the first
// existing candidate, preferring a path that contains "forever".
func Detect(configured string, candidates []string) (string, error) {
	return DetectClient(configured, candidates, "forever")
}

// DetectClient prefers the folder for client: forever, retail, classic_era
// (_classic_era_), classic (_classic_), or anniversary (_anniversary_).
// A missing preferred folder still resolves to another installed flavor.
func DetectClient(configured string, candidates []string, client string) (string, error) {
	client = NormalizeClient(client)
	if !ValidClient(client) {
		return "", errors.New("ukjent klient. Bruk forever, retail, classic_era eller classic")
	}
	if strings.TrimSpace(configured) != "" {
		if p, ok := ResolveClient(configured, client); ok {
			return p, nil
		}
		return "", errors.New("wow_path peker ikke på en WoW-mappe med Interface/AddOns")
	}
	var found []string
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if p, ok := ResolveClient(c, client); ok && !seen[p] {
			seen[p] = true
			found = append(found, p)
		}
	}
	if len(found) == 0 {
		return "", errors.New("fant ikke World of Warcraft. Sett --wow-path eller wow_path i config.json")
	}
	sort.SliceStable(found, func(i, j int) bool { return score(found[i], client) > score(found[j], client) })
	return found[0], nil
}

// Flavor is one WoW client folder that has Interface/AddOns.
type Flavor struct {
	Path   string
	Client string // forever, retail, classic_era, classic, anniversary
}

// ClientOf returns the install mode for a flavor path. Unknown folders use forever.
func ClientOf(path string) string {
	if k := flavorKind(path); k != "" {
		return k
	}
	return "forever"
}

func isFlavorFolderName(base string) bool {
	b := strings.ToLower(strings.TrimSpace(base))
	switch b {
	case "_forever_", "forever", "_retail_", "_classic_era_", "_classic_", "_anniversary_", "classic":
		return true
	}
	return strings.HasPrefix(b, "_") && strings.HasSuffix(b, "_") && flavorKind(base) != ""
}

// InstallRoot is the Battle.net install folder to persist in wow_path.
// A path like .../World of Warcraft/_classic_era_ becomes .../World of Warcraft.
// A standalone "World of Warcraft Forever" folder stays itself.
func InstallRoot(path string) string {
	path = filepath.Clean(path)
	if path == "" || path == "." {
		return path
	}
	if isFlavor(path) && isFlavorFolderName(filepath.Base(path)) {
		return filepath.Dir(path)
	}
	return path
}

func flavorsInDir(dir string) []Flavor {
	dir = filepath.Clean(dir)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var found []Flavor
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		candidate := filepath.Join(dir, e.Name())
		if !isFlavor(candidate) {
			continue
		}
		found = append(found, Flavor{Path: candidate, Client: ClientOf(candidate)})
	}
	sort.SliceStable(found, func(i, j int) bool {
		return score(found[i].Path, "forever") > score(found[j].Path, "forever")
	})
	return found
}

// ListFlavors returns every flavor under a configured path. A flavor folder
// (.../_classic_era_) includes its siblings under the same install root.
func ListFlavors(path string) []Flavor {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "" || path == "." {
		return nil
	}
	root := InstallRoot(path)
	if kids := flavorsInDir(root); len(kids) > 0 {
		return kids
	}
	if isFlavor(path) {
		return []Flavor{{Path: path, Client: ClientOf(path)}}
	}
	if root != path && isFlavor(root) {
		return []Flavor{{Path: root, Client: ClientOf(root)}}
	}
	return nil
}

// DetectAll lists every installed flavor. A non-empty configured path limits
// discovery to that install (and its sibling flavors). Empty configured scans candidates.
func DetectAll(configured string, candidates []string) ([]Flavor, error) {
	if strings.TrimSpace(configured) != "" {
		list := ListFlavors(configured)
		if len(list) == 0 {
			return nil, errors.New("wow_path peker ikke på en WoW-mappe med Interface/AddOns")
		}
		return list, nil
	}
	var found []Flavor
	seen := map[string]bool{}
	for _, c := range candidates {
		if c == "" {
			continue
		}
		for _, f := range ListFlavors(c) {
			if seen[f.Path] {
				continue
			}
			seen[f.Path] = true
			found = append(found, f)
		}
	}
	if len(found) == 0 {
		return nil, errors.New("fant ikke World of Warcraft. Sett --wow-path eller wow_path i config.json")
	}
	sort.SliceStable(found, func(i, j int) bool {
		return score(found[i].Path, "forever") > score(found[j].Path, "forever")
	})
	return found, nil
}

func AddonDir(wow string) string {
	return filepath.Join(wow, "Interface", "AddOns", "Sagalore")
}

func ChatLog(wow string) string { return filepath.Join(wow, "Logs", "WoWChatLog.txt") }

func Screenshots(wow string) string { return filepath.Join(wow, "Screenshots") }
