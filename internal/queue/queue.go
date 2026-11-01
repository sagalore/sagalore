// Package queue persists completion and level events until the API accepts them.
package queue

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/sagalore/sagalore/internal/contract"
)

type Item struct {
	Kind           string                 `json:"kind"`
	Key            string                 `json:"key"`
	QuestID        string                 `json:"saga_id,omitempty"`
	QuestVersion   int                    `json:"saga_version,omitempty"`
	QuestTitle     string                 `json:"saga_title,omitempty"`
	Character      string                 `json:"character"`
	Realm          string                 `json:"realm,omitempty"`
	Race           string                 `json:"race,omitempty"`
	Class          string                 `json:"class,omitempty"`
	BattleTag      string                 `json:"battletag,omitempty"`
	CharacterID    string                 `json:"character_id,omitempty"`
	Level          int                    `json:"level,omitempty"`
	CompletedAt    string                 `json:"completed_at,omitempty"`
	StartedAt      string                 `json:"started_at,omitempty"`
	Instance       string                 `json:"instance,omitempty"`
	GroupMembers   []contract.GroupMember `json:"group_members,omitempty"`
	ScreenshotPath string                 `json:"screenshot_path,omitempty"`
	Action         string                 `json:"action,omitempty"`
	InviteID       string                 `json:"invite_id,omitempty"`
	Attempts       int                    `json:"attempts"`
	NextAttempt    time.Time              `json:"next_attempt"`
	LastError      string                 `json:"last_error,omitempty"`
	Dead           bool                   `json:"dead,omitempty"`
}

type fileState struct {
	Items []Item          `json:"items"`
	Done  map[string]bool `json:"done"`
}

type Queue struct {
	mu    sync.Mutex
	path  string
	items []Item
	done  map[string]bool
}

func Open(path string) (*Queue, error) {
	q := &Queue{path: path, done: map[string]bool{}}
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return q, nil
		}
		return nil, err
	}
	var st fileState
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, err
	}
	q.items = st.Items
	if st.Done != nil {
		q.done = st.Done
	}
	return q, nil
}

// CompletionKey dedupes one completion per quest and character (realm included
// so the same name on two realms is not the same character).
func CompletionKey(questID, character, realm string) string {
	return "completion\n" + questID + "\n" + strings.ToLower(character) + "\n" + strings.ToLower(realm)
}

func CharacterKey(name, realm string, level int, race, className, tag string) string {
	return strings.Join([]string{
		"character", strings.ToLower(name), strings.ToLower(realm), itoa(level),
		strings.ToLower(race), strings.ToLower(className), strings.ToLower(tag),
	}, "\n")
}

func LevelKey(character, realm string, level int) string {
	return "level\n" + strings.ToLower(character) + "\n" + strings.ToLower(realm) + "\n" + itoa(level)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// Enqueue adds an item unless the key is already queued or finished.
// It returns false when the event is a duplicate.
func (q *Queue) Enqueue(item Item) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if item.Key == "" || q.done[item.Key] {
		return false
	}
	for _, existing := range q.items {
		if existing.Key == item.Key {
			return false
		}
	}
	q.items = append(q.items, item)
	return true
}

// SetStartedAt fills a start time on a completion that is still queued.
// It returns false when that key is not waiting or already has a start.
func (q *Queue) SetStartedAt(key, started string) bool {
	started = strings.TrimSpace(started)
	if key == "" || started == "" {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.items {
		if q.items[i].Key != key || q.items[i].StartedAt != "" {
			continue
		}
		q.items[i].StartedAt = started
		return true
	}
	return false
}

// SetGroup attaches a roster to a completion that is still queued.
// It returns false when that key is not waiting (already sent or never queued).
func (q *Queue) SetGroup(key, instance string, members []contract.GroupMember) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.items {
		if q.items[i].Key != key {
			continue
		}
		q.items[i].Instance = instance
		q.items[i].GroupMembers = members
		return true
	}
	return false
}

// ForgetDone removes a finished key so the same event can be posted again
// (e.g. after progress was cleared on the server).
func (q *Queue) ForgetDone(key string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	delete(q.done, key)
}

// ForgetDonePrefix drops every finished key with the given prefix
// (e.g. "completion\n"+questID after a /sagalore reset).
func (q *Queue) ForgetDonePrefix(prefix string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if prefix == "" {
		return
	}
	for k := range q.done {
		if strings.HasPrefix(k, prefix) {
			delete(q.done, k)
		}
	}
}

// MarkDone records a key as finished without a queued item (used for resets).
func (q *Queue) MarkDone(key string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if key == "" {
		return
	}
	q.done[key] = true
}

// IsDone reports whether key was already finished.
func (q *Queue) IsDone(key string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.done[key]
}

// ResetKey dedupes one addon-queued progress reset.
func ResetKey(questID string, all bool, at int64) string {
	if all || strings.TrimSpace(questID) == "" {
		return "reset\nall\n" + itoa(int(at))
	}
	return "reset\n" + questID + "\n" + itoa(int(at))
}

func (q *Queue) Due(now time.Time) []Item {
	q.mu.Lock()
	defer q.mu.Unlock()
	var out []Item
	for _, item := range q.items {
		if item.Dead {
			continue
		}
		if item.NextAttempt.IsZero() || !item.NextAttempt.After(now) {
			out = append(out, item)
		}
	}
	return out
}

func (q *Queue) Succeed(key string) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.done[key] = true
	q.drop(key)
	if len(q.done) > 5000 {
		next := map[string]bool{key: true}
		for k := range q.done {
			if len(next) >= 4000 {
				break
			}
			next[k] = true
		}
		q.done = next
	}
}

func (q *Queue) Fail(key, message string, permanent bool, now time.Time) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.items {
		if q.items[i].Key != key {
			continue
		}
		q.items[i].Attempts++
		q.items[i].LastError = message
		if permanent || q.items[i].Attempts >= 20 {
			q.items[i].Dead = true
			return
		}
		q.items[i].NextAttempt = now.Add(backoff(q.items[i].Attempts))
		return
	}
}

func (q *Queue) Update(item Item) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for i := range q.items {
		if q.items[i].Key == item.Key {
			item.Attempts = q.items[i].Attempts
			item.NextAttempt = q.items[i].NextAttempt
			item.Dead = q.items[i].Dead
			item.LastError = q.items[i].LastError
			q.items[i] = item
			return
		}
	}
}

func (q *Queue) drop(key string) {
	out := q.items[:0]
	for _, item := range q.items {
		if item.Key != key {
			out = append(out, item)
		}
	}
	q.items = out
}

func backoff(attempts int) time.Duration {
	d := 5 * time.Second
	for i := 1; i < attempts; i++ {
		d *= 3
		if d > 15*time.Minute {
			return 15 * time.Minute
		}
	}
	return d
}

func (q *Queue) Save() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if err := os.MkdirAll(filepath.Dir(q.path), 0o755); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(fileState{Items: q.items, Done: q.done}, "", "  ")
	if err != nil {
		return err
	}
	tmp := q.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		_ = os.Remove(q.path)
	}
	return os.Rename(tmp, q.path)
}

func (q *Queue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}
