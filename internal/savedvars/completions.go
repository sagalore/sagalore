// Package savedvars reads Sagalore completions from WoW SavedVariables.
// Classic Era does not write DEFAULT_CHAT_FRAME:AddMessage into WoWChatLog.txt,
// so the companion also polls these files for COMPLETE events.
package savedvars

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/sagalore/sagalore/internal/contract"
)

// Completion is one local finish stored by the addon under SagaloreDB.completions.
type Completion struct {
	QuestID      string
	QuestVersion int
	Character    string
	Realm        string
	Race         string
	Class        string
	Level        int
	At           time.Time
	Started      time.Time
	Path         string
	Instance     string
	GroupMembers []contract.GroupMember
}

var (
	completionBlock = regexp.MustCompile(`(?s)\["([0-9a-fA-F-]{36})"\]\s*=\s*\{(.*?)\},`)
	fieldString     = regexp.MustCompile(`\["(saga_id|character|realm|race|class|group_members|instance)"\]\s*=\s*"([^"]*)"`)
	fieldNumber     = regexp.MustCompile(`\["(version|level|completed_at|started_at)"\]\s*=\s*(-?\d+)`)
)

// ListSagaloreFiles returns SavedVariables/Sagalore.lua paths under the WoW account tree.
func ListSagaloreFiles(wowDir string) ([]string, error) {
	root := filepath.Join(wowDir, "WTF", "Account")
	var out []string
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		if d.IsDir() || !inSavedVariables(path) {
			return nil
		}
		if strings.EqualFold(d.Name(), "Sagalore.lua") {
			out = append(out, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func inSavedVariables(path string) bool {
	sep := string(filepath.Separator) + "SavedVariables" + string(filepath.Separator)
	return strings.Contains(path, sep)
}

// ParseCompletions extracts SagaloreDB.completions entries from one SavedVariables file.
func ParseCompletions(path string, body string) []Completion {
	start := strings.Index(body, `["completions"]`)
	if start < 0 {
		start = strings.Index(body, "completions")
	}
	if start < 0 {
		return nil
	}
	body = body[start:]
	end := strings.Index(body, `["objectives"]`)
	if end < 0 {
		end = strings.Index(body, `["sections"]`)
	}
	if end > 0 {
		body = body[:end]
	}
	var out []Completion
	for _, m := range completionBlock.FindAllStringSubmatch(body, -1) {
		block := m[2]
		c := Completion{QuestID: m[1], Path: path, Level: 1}
		for _, f := range fieldString.FindAllStringSubmatch(block, -1) {
			switch f[1] {
			case "saga_id":
				if f[2] != "" {
					c.QuestID = f[2]
				}
			case "character":
				c.Character = f[2]
			case "realm":
				c.Realm = f[2]
			case "race":
				c.Race = f[2]
			case "class":
				c.Class = f[2]
			case "instance":
				c.Instance = f[2]
			case "group_members":
				c.GroupMembers = parseMemberBlob(f[2])
			}
		}
		for _, f := range fieldNumber.FindAllStringSubmatch(block, -1) {
			n, err := strconv.ParseInt(f[2], 10, 64)
			if err != nil {
				continue
			}
			switch f[1] {
			case "version":
				c.QuestVersion = int(n)
			case "level":
				c.Level = int(n)
			case "completed_at":
				if n > 0 {
					c.At = time.Unix(n, 0).UTC()
				}
			case "started_at":
				if n > 0 {
					c.Started = time.Unix(n, 0).UTC()
				}
			}
		}
		if c.QuestID == "" || c.Character == "" {
			continue
		}
		if c.At.IsZero() {
			c.At = time.Now().UTC()
		}
		if c.Level < 1 {
			c.Level = 1
		}
		out = append(out, c)
	}
	return out
}

func parseMemberBlob(s string) []contract.GroupMember {
	if s == "" {
		return nil
	}
	var out []contract.GroupMember
	for _, part := range strings.Split(s, ";") {
		bits := strings.Split(part, "/")
		if len(bits) != 3 || strings.TrimSpace(bits[0]) == "" {
			continue
		}
		level, err := strconv.Atoi(bits[2])
		if err != nil || level < 0 {
			level = 0
		}
		out = append(out, contract.GroupMember{Name: bits[0], Realm: bits[1], Level: level})
	}
	return out
}


func objectivesHaveData(body string) bool {
	i := strings.Index(body, `["objectives"]`)
	if i < 0 {
		return false
	}
	rest := body[i:]
	return strings.Contains(rest, `["steps"]`) || strings.Contains(rest, `["done"]`)
}

// ReadCompletions loads completions from every Sagalore SavedVariables file under wowDir.
func ReadCompletions(wowDir string) ([]Completion, error) {
	files, err := ListSagaloreFiles(wowDir)
	if err != nil {
		return nil, err
	}
	var out []Completion
	for _, path := range files {
		b, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		out = append(out, ParseCompletions(path, string(b))...)
	}
	return out, nil
}
