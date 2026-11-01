package savedvars

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// PendingInvite is one accept/decline the addon queued under SagaloreDB.pending_invites.
// Classic Era chat logging does not capture AddMessage, so the companion polls these.
type PendingInvite struct {
	Action string
	ID     string
	At     int64
	Path   string
}

var (
	inviteBlock  = regexp.MustCompile(`(?s)\{\s*(.*?)\},`)
	inviteAction = regexp.MustCompile(`\["action"\]\s*=\s*"(accept|decline)"`)
	inviteID     = regexp.MustCompile(`\["id"\]\s*=\s*"([^"]+)"`)
	inviteAt     = regexp.MustCompile(`\["at"\]\s*=\s*(-?\d+)`)
)

// ParsePendingInvites extracts SagaloreDB.pending_invites from one SavedVariables file.
func ParsePendingInvites(path, body string) []PendingInvite {
	start := strings.Index(body, `["pending_invites"]`)
	if start < 0 {
		start = strings.Index(body, "pending_invites")
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
	var out []PendingInvite
	for _, m := range inviteBlock.FindAllStringSubmatch(inner, -1) {
		block := m[1]
		inv := PendingInvite{Path: path}
		if a := inviteAction.FindStringSubmatch(block); len(a) == 2 {
			inv.Action = a[1]
		}
		if id := inviteID.FindStringSubmatch(block); len(id) == 2 {
			inv.ID = strings.TrimSpace(id[1])
		}
		if at := inviteAt.FindStringSubmatch(block); len(at) == 2 {
			n, _ := strconv.ParseInt(at[1], 10, 64)
			inv.At = n
		}
		if inv.ID == "" || (inv.Action != "accept" && inv.Action != "decline") {
			continue
		}
		out = append(out, inv)
	}
	return out
}

// ReadPendingInvites loads pending invite answers from every Sagalore SavedVariables file.
func ReadPendingInvites(wowDir string) ([]PendingInvite, error) {
	files, err := ListSagaloreFiles(wowDir)
	if err != nil {
		return nil, err
	}
	var out []PendingInvite
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out = append(out, ParsePendingInvites(path, string(raw))...)
	}
	return out, nil
}
