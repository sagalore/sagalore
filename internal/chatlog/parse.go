// Package chatlog parses Sagalore lines from WoWChatLog.txt.
package chatlog

import (
	"strconv"
	"strings"
	"time"
)

type Kind string

const (
	KindComplete  Kind = "completion"
	KindLevel     Kind = "level"
	KindCharacter Kind = "character"
	KindInvite    Kind = "invite"
)

// Event is one machine-readable chat line.
// COMPLETE <questId> <version> <character> <realm...> <level> <unix>
// LEVELUP <character> <realm...> <level> <unix>
// CHARACTER name|realm|level|race|class|battletag|unix
type Event struct {
	Kind         Kind
	QuestID      string
	QuestVersion int
	Character    string
	Realm        string
	Level        int
	Race         string
	Class        string
	BattleTag    string
	At           time.Time
	Action       string
	InviteID     string
}

func Parse(line string) (Event, bool) {
	// A channel echo can contain the prefix twice; the machine verb is the one that counts.
	const marker = "[Sagalore]"
	rest := line
	for {
		i := strings.Index(rest, marker)
		if i < 0 {
			return Event{}, false
		}
		payload := strings.TrimSpace(rest[i+len(marker):])
		if ev, ok := parsePayload(payload); ok {
			return ev, true
		}
		rest = rest[i+len(marker):]
	}
}

func parsePayload(rest string) (Event, bool) {
	if strings.HasPrefix(rest, "CHARACTER ") {
		return parseCharacter(rest[len("CHARACTER "):])
	}
	if strings.HasPrefix(rest, "INVITE ") {
		return parseInvite(rest[len("INVITE "):])
	}
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return Event{}, false
	}
	switch fields[0] {
	case "COMPLETE":
		return parseComplete(fields)
	case "LEVELUP":
		return parseLevel(fields)
	}
	return Event{}, false
}

func parseInvite(payload string) (Event, bool) {
	action, id, ok := strings.Cut(strings.TrimSpace(payload), "|")
	if !ok || (action != "accept" && action != "decline") || strings.TrimSpace(id) == "" {
		return Event{}, false
	}
	return Event{Kind: KindInvite, Action: action, InviteID: strings.TrimSpace(id)}, true
}

func parseComplete(fields []string) (Event, bool) {
	// COMPLETE quest version character realm... level ts
	if len(fields) < 7 {
		return Event{}, false
	}
	version, err1 := strconv.Atoi(fields[2])
	level, err2 := strconv.Atoi(fields[len(fields)-2])
	ts, err3 := strconv.ParseInt(fields[len(fields)-1], 10, 64)
	if err1 != nil || err2 != nil || err3 != nil || !valid(level, ts) || version < 0 {
		return Event{}, false
	}
	realm := strings.Join(fields[4:len(fields)-2], " ")
	if realm == "-" {
		realm = ""
	}
	if fields[1] == "" || fields[3] == "" {
		return Event{}, false
	}
	return Event{
		Kind: KindComplete, QuestID: fields[1], QuestVersion: version,
		Character: fields[3], Realm: realm, Level: level, At: time.Unix(ts, 0).UTC(),
	}, true
}

func parseLevel(fields []string) (Event, bool) {
	// LEVELUP character realm... level ts
	if len(fields) < 5 {
		return Event{}, false
	}
	level, err1 := strconv.Atoi(fields[len(fields)-2])
	ts, err2 := strconv.ParseInt(fields[len(fields)-1], 10, 64)
	if err1 != nil || err2 != nil || !valid(level, ts) || fields[1] == "" {
		return Event{}, false
	}
	realm := strings.Join(fields[2:len(fields)-2], " ")
	if realm == "-" {
		realm = ""
	}
	return Event{
		Kind: KindLevel, Character: fields[1], Realm: realm, Level: level, At: time.Unix(ts, 0).UTC(),
	}, true
}

func parseCharacter(payload string) (Event, bool) {
	parts := strings.Split(payload, "|")
	if len(parts) != 7 {
		return Event{}, false
	}
	level, err1 := strconv.Atoi(strings.TrimSpace(parts[2]))
	ts, err2 := strconv.ParseInt(strings.TrimSpace(parts[6]), 10, 64)
	name := field(parts[0])
	if err1 != nil || err2 != nil || !valid(level, ts) || name == "" {
		return Event{}, false
	}
	tag := field(parts[5])
	if tag != "" && !strings.Contains(tag, "#") {
		return Event{}, false
	}
	return Event{
		Kind: KindCharacter, Character: name, Realm: field(parts[1]), Level: level,
		Race: field(parts[3]), Class: field(parts[4]), BattleTag: tag, At: time.Unix(ts, 0).UTC(),
	}, true
}

func field(s string) string {
	s = strings.TrimSpace(s)
	if s == "-" {
		return ""
	}
	return s
}

func valid(level int, ts int64) bool {
	if level < 1 || level > 1000 {
		return false
	}
	// 2020-01-01 .. 2100-01-01
	return ts >= 1577836800 && ts <= 4102444800
}
