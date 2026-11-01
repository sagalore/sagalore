package savedvars

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// PendingCharacter is the latest toon snapshot under SagaloreDB.pending_character.
// Classic Era does not deliver CHARACTER chat lines to the companion.
type PendingCharacter struct {
	Name      string
	Realm     string
	Level     int
	Race      string
	Class     string
	BattleTag string
	At        int64
	Path      string
}

var (
	charName = regexp.MustCompile(`\["name"\]\s*=\s*"([^"]*)"`)
	charRealm = regexp.MustCompile(`\["realm"\]\s*=\s*"([^"]*)"`)
	charLevel = regexp.MustCompile(`\["level"\]\s*=\s*(-?\d+)`)
	charRace  = regexp.MustCompile(`\["race"\]\s*=\s*"([^"]*)"`)
	charClass = regexp.MustCompile(`\["class"\]\s*=\s*"([^"]*)"`)
	charTag   = regexp.MustCompile(`\["battletag"\]\s*=\s*"([^"]*)"`)
	charAt    = regexp.MustCompile(`\["at"\]\s*=\s*(-?\d+)`)
)

// ParsePendingCharacter extracts SagaloreDB.pending_character from one SavedVariables file.
func ParsePendingCharacter(path, body string) (PendingCharacter, bool) {
	start := strings.Index(body, `["pending_character"]`)
	if start < 0 {
		start = strings.Index(body, "pending_character")
	}
	if start < 0 {
		return PendingCharacter{}, false
	}
	body = body[start:]
	open := strings.Index(body, "{")
	if open < 0 {
		return PendingCharacter{}, false
	}
	depth, end := 0, -1
	for i := open; i < len(body); i++ {
		switch body[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
				break
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return PendingCharacter{}, false
	}
	block := body[open+1 : end]
	out := PendingCharacter{Path: path}
	if m := charName.FindStringSubmatch(block); len(m) == 2 {
		out.Name = strings.TrimSpace(m[1])
	}
	if m := charRealm.FindStringSubmatch(block); len(m) == 2 {
		out.Realm = strings.TrimSpace(m[1])
	}
	if m := charLevel.FindStringSubmatch(block); len(m) == 2 {
		n, _ := strconv.Atoi(m[1])
		out.Level = n
	}
	if m := charRace.FindStringSubmatch(block); len(m) == 2 {
		out.Race = strings.TrimSpace(m[1])
	}
	if m := charClass.FindStringSubmatch(block); len(m) == 2 {
		out.Class = strings.TrimSpace(m[1])
	}
	if m := charTag.FindStringSubmatch(block); len(m) == 2 {
		out.BattleTag = strings.TrimSpace(m[1])
	}
	if m := charAt.FindStringSubmatch(block); len(m) == 2 {
		n, _ := strconv.ParseInt(m[1], 10, 64)
		out.At = n
	}
	if out.Name == "" || out.Level < 1 {
		return PendingCharacter{}, false
	}
	return out, true
}

// ReadPendingCharacters loads pending_character from every Sagalore SavedVariables file.
func ReadPendingCharacters(wowDir string) ([]PendingCharacter, error) {
	files, err := ListSagaloreFiles(wowDir)
	if err != nil {
		return nil, err
	}
	var out []PendingCharacter
	seen := map[string]bool{}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		ch, ok := ParsePendingCharacter(path, string(raw))
		if !ok {
			continue
		}
		key := strings.ToLower(ch.Name) + "\n" + strings.ToLower(ch.Realm)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, ch)
	}
	return out, nil
}
