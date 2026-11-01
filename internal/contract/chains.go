package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

// A prerequisite counts when that character has any completed progress row
// for the saga id. quest_version on that row is not consulted: finishing an
// older version still unlocks whatever currently requires it.

var (
	requireUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	chainIDRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

// Chain groups sagas for the log. Index and Total are computed when the saga
// is read (1-based rank and how many sagas share the id). They are ignored
// on create and update.
type Chain struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Order int    `json:"order"`
	Index int    `json:"index,omitempty"`
	Total int    `json:"total,omitempty"`
}

// Require is one prerequisite as the addon sees it (id plus a title for the
// lock line). The authoring API takes and returns bare saga ids.
type Require struct {
	ID    string `json:"id"`
	Title string `json:"title,omitempty"`
}

// SagaLock is locked/unlocked for one character and one saga.
// Locked is false when the saga is already completed, even if requires were
// added later. Missing lists prerequisite ids that still block a new completion.
type SagaLock struct {
	QuestID     string   `json:"saga_id"`
	CharacterID string   `json:"character_id"`
	Locked      bool     `json:"locked"`
	Unlocked    bool     `json:"unlocked"`
	Missing     []string `json:"missing,omitempty"`
}

// ParseRequires accepts a JSON array of saga ids, or objects with an id
// field (so a GET body can be sent back). Omitted raw means not set.
func ParseRequires(raw json.RawMessage) (ids []string, set bool, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if string(trimmed) == "null" {
		return []string{}, true, nil
	}
	var items []json.RawMessage
	if err := json.Unmarshal(trimmed, &items); err != nil {
		return nil, true, errors.New("requires")
	}
	ids = make([]string, 0, len(items))
	for _, item := range items {
		item = bytes.TrimSpace(item)
		if len(item) == 0 {
			return nil, true, errors.New("requires")
		}
		if item[0] == '"' {
			var s string
			if err := json.Unmarshal(item, &s); err != nil {
				return nil, true, errors.New("requires")
			}
			ids = append(ids, s)
			continue
		}
		var obj struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(item, &obj); err != nil || strings.TrimSpace(obj.ID) == "" {
			return nil, true, errors.New("requires")
		}
		ids = append(ids, obj.ID)
	}
	return ids, true, nil
}

// ParseChain accepts a chain object or null. Omitted raw means not set.
func ParseChain(raw json.RawMessage) (chain *Chain, set bool, err error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, false, nil
	}
	if string(trimmed) == "null" {
		return nil, true, nil
	}
	var c Chain
	if err := json.Unmarshal(trimmed, &c); err != nil {
		return nil, true, errors.New("chain")
	}
	return &c, true, nil
}

// NormalizeRequires checks ids, drops duplicates, and lowercases them so they
// match Postgres uuid text. At most 20 prerequisites.
func NormalizeRequires(ids []string) ([]string, error) {
	if len(ids) > 20 {
		return nil, errors.New("too many prerequisites")
	}
	out := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.ToLower(strings.TrimSpace(id))
		if !requireUUID.MatchString(id) {
			return nil, errors.New("prerequisite id")
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// NormalizeChain checks id, name, and order. Index and total are dropped.
func NormalizeChain(c *Chain) (*Chain, error) {
	if c == nil {
		return nil, nil
	}
	id := strings.TrimSpace(c.ID)
	name := strings.TrimSpace(c.Name)
	if !chainIDRe.MatchString(id) {
		return nil, errors.New("chain id")
	}
	if err := chainName(name); err != nil {
		return nil, err
	}
	if c.Order < 1 || c.Order > 10000 {
		return nil, errors.New("chain order")
	}
	return &Chain{ID: id, Name: name, Order: c.Order}, nil
}

func chainName(name string) error {
	n := []rune(name)
	if len(n) < 1 || len(n) > 80 {
		return errors.New("chain name")
	}
	for _, r := range n {
		if r == '\n' || r == '\r' || unicode.IsControl(r) {
			return errors.New("chain name")
		}
	}
	if !utf8.ValidString(name) {
		return errors.New("chain name")
	}
	return nil
}

// HasRequireCycle reports whether quest id → prerequisite ids contains a cycle.
func HasRequireCycle(graph map[string][]string) bool {
	color := map[string]int{}
	var dfs func(string) bool
	dfs = func(id string) bool {
		switch color[id] {
		case 1:
			return true
		case 2:
			return false
		}
		color[id] = 1
		for _, next := range graph[id] {
			if dfs(next) {
				return true
			}
		}
		color[id] = 2
		return false
	}
	for id := range graph {
		if dfs(id) {
			return true
		}
	}
	return false
}

// MissingRequires returns prerequisite ids that are not in done, in order.
func MissingRequires(requires []string, done map[string]bool) []string {
	var missing []string
	for _, id := range requires {
		if done == nil || !done[id] {
			missing = append(missing, id)
		}
	}
	return missing
}

// Gate is the lock for one saga. An already completed saga is unlocked even
// if a prerequisite was added afterwards.
func Gate(questID string, requires []string, done map[string]bool) (locked bool, missing []string) {
	if done != nil && done[questID] {
		return false, nil
	}
	missing = MissingRequires(requires, done)
	if len(missing) == 0 {
		return false, nil
	}
	return true, missing
}

// SagaLocks is one row per character and saga.
func SagaLocks(characters []Character, quests []Quest, progress []Progress) []SagaLock {
	doneBy := map[string]map[string]bool{}
	for _, p := range progress {
		if p.Status != "completed" {
			continue
		}
		if doneBy[p.CharacterID] == nil {
			doneBy[p.CharacterID] = map[string]bool{}
		}
		doneBy[p.CharacterID][p.QuestID] = true
	}
	out := make([]SagaLock, 0, len(characters)*len(quests))
	for _, ch := range characters {
		for _, q := range quests {
			locked, missing := Gate(q.ID, q.Requires, doneBy[ch.ID])
			out = append(out, SagaLock{
				QuestID: q.ID, CharacterID: ch.ID,
				Locked: locked, Unlocked: !locked, Missing: missing,
			})
		}
	}
	return out
}

// LockMessage is the addon line for the prerequisites still open.
// One name: "Locked: finish X first". Several: "Locked: finish A, B and C first".
func LockMessage(titles []string) string {
	clean := make([]string, 0, len(titles))
	for _, t := range titles {
		t = strings.TrimSpace(t)
		if t == "" {
			t = "a saga"
		}
		clean = append(clean, t)
	}
	if len(clean) == 0 {
		return ""
	}
	return "Locked: finish " + joinEnglish(clean) + " first"
}

func joinEnglish(parts []string) string {
	switch len(parts) {
	case 0:
		return ""
	case 1:
		return parts[0]
	case 2:
		return parts[0] + " and " + parts[1]
	default:
		return strings.Join(parts[:len(parts)-1], ", ") + " and " + parts[len(parts)-1]
	}
}
