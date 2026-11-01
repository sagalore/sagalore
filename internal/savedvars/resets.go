package savedvars

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// ResetRequest is one /sagalore (or /sl) reset the addon queued for the companion.
// QuestID empty (or All) means wipe every progress row for the player.
type ResetRequest struct {
	QuestID string
	All     bool
	At      int64
	Path    string
}

var (
	resetBlock = regexp.MustCompile(`(?s)\{\s*(.*?)\},`)
	resetAll   = regexp.MustCompile(`\["all"\]\s*=\s*true`)
	resetQuest = regexp.MustCompile(`\["saga_id"\]\s*=\s*"([^"]+)"`)
	resetAt    = regexp.MustCompile(`\["at"\]\s*=\s*(-?\d+)`)
)

// ParseResets extracts SagaloreDB.pending_resets from one SavedVariables file.
func ParseResets(path, body string) []ResetRequest {
	start := strings.Index(body, `["pending_resets"]`)
	if start < 0 {
		start = strings.Index(body, "pending_resets")
	}
	if start < 0 {
		return nil
	}
	body = body[start:]
	open := strings.Index(body, "{")
	if open < 0 {
		return nil
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
		return nil
	}
	inner := body[open+1 : end]
	var out []ResetRequest
	for _, m := range resetBlock.FindAllStringSubmatch(inner, -1) {
		block := m[1]
		req := ResetRequest{Path: path}
		if resetAll.MatchString(block) {
			req.All = true
		}
		if q := resetQuest.FindStringSubmatch(block); len(q) == 2 {
			req.QuestID = strings.TrimSpace(q[1])
		}
		if a := resetAt.FindStringSubmatch(block); len(a) == 2 {
			n, _ := strconv.ParseInt(a[1], 10, 64)
			req.At = n
		}
		if !req.All && req.QuestID == "" {
			continue
		}
		out = append(out, req)
	}
	return out
}

// ReadResets loads pending reset requests from every Sagalore SavedVariables file.
func ReadResets(wowDir string) ([]ResetRequest, error) {
	files, err := ListSagaloreFiles(wowDir)
	if err != nil {
		return nil, err
	}
	var out []ResetRequest
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out = append(out, ParseResets(path, string(raw))...)
	}
	return out, nil
}
