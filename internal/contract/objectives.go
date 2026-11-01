package contract

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// Objective types the addon can verify.
//
// Author steps with go, talk, item, kill, emote, and sequence.
// location, interact, and collect are the same steps under older names.
// all_of and any_of still evaluate, but new sagas should not use them:
// the saga itself is the AND, and a timed order is one sequence step.
//
//	go:            be at a place. memory visit (default) latches; hold is only while there
//	talk:          dialogue, object use, or /talk on that unit (right-click counts). Always visit
//	item:          have N of an item. memory hold (default) clears when the bags do; visit latches
//	kill:          defeat N creatures by name. Always visit. Optional group gate
//	emote:         a built-in emote token, optionally at a unit and/or place. Always visit
//	sequence:      ordered beats sharing one within_sec window. Always visit once finished
//	level:         reach character level N. Always visit. Optional when.ends_at for a deadline
//	instance_run:  leave N dungeon/raid instances. Optional kinds + soft group.size. Always visit
//	all_of:        legacy simultaneous AND
//	any_of:        legacy alternatives
//
// Optional when on an objective gates it to a clock window and/or an absolute range.
// By default goals may be completed in any order (quest.objectives_ordered=false).
// Order inside one trick belongs on a sequence step, not objectives_ordered.
const (
	ObjectiveGo          = "go"
	ObjectiveTalk        = "talk"
	ObjectiveItem        = "item"
	ObjectiveLocation    = "location"
	ObjectiveInteract    = "interact"
	ObjectiveCollect     = "collect"
	ObjectiveKill        = "kill"
	ObjectiveEmote       = "emote"
	ObjectiveSequence    = "sequence"
	ObjectiveLevel       = "level"
	ObjectiveInstanceRun = "instance_run"
	ObjectiveAllOf       = "all_of"
	ObjectiveAnyOf       = "any_of"
	objectiveLegacy      = "legacy"

	MemoryVisit = "visit"
	MemoryHold  = "hold"
)

// Clock names for a time window. The addon reads them as follows:
//
//   - game:  GetGameTime — the in-game day/night clock, not a real date
//   - local: time and date("*t") — the computer's clock (the default)
//   - realm: GetServerTime formatted with date("!*t") as UTC
const (
	ClockGame  = "game"
	ClockLocal = "local"
	ClockRealm = "realm"
)

// DefaultRadius is the map-fraction distance used when a location objective
// sets x and y but omits radius. Map coordinates are 0–1.
const DefaultRadius = 0.02

const (
	maxObjectives = 20
	maxNodes      = 40
	maxDepth      = 4
)

var (
	objectiveID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
	emoteToken  = regexp.MustCompile(`^[A-Z0-9_]{1,32}$`)
	hhmm        = regexp.MustCompile(`^(?:[01][0-9]|2[0-3]):[0-5][0-9]$`)
)

// TimeWindow is an optional gate on an objective or a whole saga.
// A time-of-day window may cross midnight (22:00–02:00). starts_at/ends_at are
// absolute instants. When both are set they are AND. clock "game" cannot carry
// a calendar range because GetGameTime has no date.
type TimeWindow struct {
	Clock    string `json:"clock,omitempty"`
	Start    string `json:"start,omitempty"`
	End      string `json:"end,omitempty"`
	StartsAt string `json:"starts_at,omitempty"`
	EndsAt   string `json:"ends_at,omitempty"`
}

// GroupRule is the saga-level party requirement checked when the saga completes.
// size is how many group members (including the player) must be present.
// min_level applies to each member who counts. instance matches GetInstanceInfo
// or the zone name (same | / ; alternatives as NPC names).
type GroupRule struct {
	Size     int    `json:"size"`
	MinLevel *int   `json:"min_level,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// GroupMember is one character the addon saw in the party or raid.
type GroupMember struct {
	Name  string `json:"name"`
	Realm string `json:"realm,omitempty"`
	Level int    `json:"level,omitempty"`
}

// GroupCredit is a character the API marked complete because they were in the
// finishing group. already is true when that character had already finished.
type GroupCredit struct {
	CharacterID string `json:"character_id"`
	Name        string `json:"name"`
	Realm       string `json:"realm,omitempty"`
	Already     bool   `json:"already"`
}

// StepGroup is a gate on a kill step. level is exact for every party member.
// size, when set, is the exact roster size. instance matches GetInstanceInfo
// or the zone. Each member's addon must attest from inside that instance
// within 15 seconds of the death. The kill latches only when the gate passes.
type StepGroup struct {
	Level    int    `json:"level"`
	Size     int    `json:"size,omitempty"`
	Instance string `json:"instance,omitempty"`
}

// Objective is one step of a quest. LLMs author these through the admin API.
// The same array is snapshotted on every quest version.
// Children are used only by legacy all_of and any_of. Sequence steps use beats.
type Objective struct {
	ID        string      `json:"id"`
	Type      string      `json:"type"`
	Text      string      `json:"text"`
	Memory    string      `json:"memory,omitempty"`
	Zone      string      `json:"zone,omitempty"`
	Subzone   string      `json:"subzone,omitempty"`
	MapID     *int        `json:"map_id,omitempty"`
	X         *float64    `json:"x,omitempty"`
	Y         *float64    `json:"y,omitempty"`
	Radius    *float64    `json:"radius,omitempty"`
	NPC       string      `json:"npc,omitempty"`
	Object    string      `json:"object,omitempty"`
	ItemID    *int        `json:"item_id,omitempty"`
	ItemName  string      `json:"item_name,omitempty"`
	Count     int         `json:"count,omitempty"`
	Level     int         `json:"level,omitempty"` // type=level target; not the kill group.level
	Kinds     []string    `json:"kinds,omitempty"` // type=instance_run: party and/or raid
	Emote     string      `json:"emote,omitempty"`
	WithinSec int         `json:"within_sec,omitempty"`
	When      *TimeWindow `json:"when,omitempty"`
	Group     *StepGroup  `json:"group,omitempty"`
	Beats     []Objective `json:"beats,omitempty"`
	Children  []Objective `json:"objectives,omitempty"`
}

// DecodeObjectives reads a jsonb value. Null and empty become an empty slice.
func DecodeObjectives(raw string) ([]Objective, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return []Objective{}, nil
	}
	var out []Objective
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	if out == nil {
		out = []Objective{}
	}
	return out, nil
}

// DecodeTimeWindow reads a jsonb object. Null and empty become nil.
func DecodeTimeWindow(raw string) (*TimeWindow, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var w TimeWindow
	if err := json.Unmarshal([]byte(raw), &w); err != nil {
		return nil, err
	}
	if w.Clock == "" && w.Start == "" && w.End == "" && w.StartsAt == "" && w.EndsAt == "" {
		return nil, nil
	}
	return &w, nil
}

// DecodeGroupRule reads a jsonb object. Null and empty become nil.
func DecodeGroupRule(raw string) (*GroupRule, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || raw == "null" {
		return nil, nil
	}
	var g GroupRule
	if err := json.Unmarshal([]byte(raw), &g); err != nil {
		return nil, err
	}
	if g.Size == 0 && g.MinLevel == nil && strings.TrimSpace(g.Instance) == "" {
		return nil, nil
	}
	return &g, nil
}

// NormalizeObjectives checks an authored objective list and fills defaults.
// legacy is accepted only so a migrated text objective can be patched forward.
func NormalizeObjectives(in []Objective, allowLegacy bool) ([]Objective, error) {
	if len(in) == 0 {
		return nil, fmt.Errorf("objectives are required")
	}
	if len(in) > maxObjectives {
		return nil, fmt.Errorf("at most %d objectives", maxObjectives)
	}
	seen := map[string]bool{}
	budget := maxNodes
	return normalizeList(in, allowLegacy, seen, 1, &budget)
}

func normalizeList(in []Objective, allowLegacy bool, seen map[string]bool, depth int, budget *int) ([]Objective, error) {
	out := make([]Objective, len(in))
	for i, o := range in {
		normed, err := normalizeOne(o, allowLegacy, seen, depth, budget)
		if err != nil {
			return nil, err
		}
		out[i] = normed
	}
	return out, nil
}

func normalizeOne(o Objective, allowLegacy bool, seen map[string]bool, depth int, budget *int) (Objective, error) {
	if depth > maxDepth {
		return Objective{}, fmt.Errorf("objective %s: nesting is deeper than %d", strings.TrimSpace(o.ID), maxDepth)
	}
	if *budget <= 0 {
		return Objective{}, fmt.Errorf("at most %d objectives including nested ones", maxNodes)
	}
	*budget--
	o.ID = strings.TrimSpace(o.ID)
	o.Type = strings.TrimSpace(o.Type)
	o.Text = strings.TrimSpace(o.Text)
	o.Zone = strings.TrimSpace(o.Zone)
	o.Subzone = strings.TrimSpace(o.Subzone)
	o.NPC = strings.TrimSpace(o.NPC)
	o.Object = strings.TrimSpace(o.Object)
	o.ItemName = strings.TrimSpace(o.ItemName)
	o.Emote = strings.TrimSpace(o.Emote)
	o.Memory = strings.ToLower(strings.TrimSpace(o.Memory))
	if !objectiveID.MatchString(o.ID) {
		return Objective{}, fmt.Errorf("objective %s: id must match [A-Za-z0-9_-]{1,40}", displayID(o.ID, depth))
	}
	if seen[o.ID] {
		return Objective{}, fmt.Errorf("objective id %s is duplicated", o.ID)
	}
	seen[o.ID] = true
	if o.Text == "" || utf8.RuneCountInString(o.Text) > 300 {
		return Objective{}, fmt.Errorf("objective %s: text is required (max 300)", o.ID)
	}
	if err := clipField(o.Zone, o.Subzone, o.NPC, o.Object, o.ItemName); err != nil {
		return Objective{}, fmt.Errorf("objective %s: %w", o.ID, err)
	}
	when, err := NormalizeTimeWindow(o.When)
	if err != nil {
		return Objective{}, fmt.Errorf("objective %s: %w", o.ID, err)
	}
	o.When = when
	switch o.Type {
	case ObjectiveGo, ObjectiveLocation:
		if err := normalizeLocation(&o); err != nil {
			return Objective{}, err
		}
		if err := finishStep(&o); err != nil {
			return Objective{}, err
		}
	case ObjectiveTalk, ObjectiveInteract:
		if o.NPC == "" && o.Object == "" {
			return Objective{}, fmt.Errorf("objective %s: talk needs npc and/or object", o.ID)
		}
		if err := finishStep(&o); err != nil {
			return Objective{}, err
		}
	case ObjectiveItem, ObjectiveCollect:
		if o.ItemID == nil || *o.ItemID <= 0 {
			return Objective{}, fmt.Errorf("objective %s: item needs item_id", o.ID)
		}
		if o.Count == 0 {
			o.Count = 1
		}
		if o.Count < 1 || o.Count > 10000 {
			return Objective{}, fmt.Errorf("objective %s: count must be 1..10000", o.ID)
		}
		if err := finishStep(&o); err != nil {
			return Objective{}, err
		}
	case ObjectiveKill:
		if o.NPC == "" {
			return Objective{}, fmt.Errorf("objective %s: kill needs npc (the creature name, not a GUID)", o.ID)
		}
		if o.Count == 0 {
			o.Count = 1
		}
		if o.Count < 1 || o.Count > 1000 {
			return Objective{}, fmt.Errorf("objective %s: count must be 1..1000", o.ID)
		}
		if o.Group != nil {
			g, err := normalizeStepGroup(o.ID, o.Group)
			if err != nil {
				return Objective{}, err
			}
			o.Group = g
		}
		if err := finishStep(&o); err != nil {
			return Objective{}, err
		}
	case ObjectiveEmote:
		token, err := NormalizeEmote(o.Emote)
		if err != nil {
			return Objective{}, fmt.Errorf("objective %s: %w", o.ID, err)
		}
		o.Emote = token
		if hasPlace(&o) {
			if err := normalizeLocation(&o); err != nil {
				return Objective{}, err
			}
		}
		if err := finishStep(&o); err != nil {
			return Objective{}, err
		}
	case ObjectiveSequence:
		if o.WithinSec < 1 || o.WithinSec > 600 {
			return Objective{}, fmt.Errorf("objective %s: within_sec must be 1..600", o.ID)
		}
		if len(o.Beats) < 2 || len(o.Beats) > 6 {
			return Objective{}, fmt.Errorf("objective %s: sequence needs 2 to 6 beats", o.ID)
		}
		if len(o.Children) > 0 {
			return Objective{}, fmt.Errorf("objective %s: sequence uses beats, not objectives", o.ID)
		}
		beats, err := normalizeList(o.Beats, allowLegacy, seen, depth+1, budget)
		if err != nil {
			return Objective{}, err
		}
		for i := range beats {
			if !sequenceBeat(beats[i].Type) {
				return Objective{}, fmt.Errorf("objective %s: sequence beats must be go, talk, emote, or kill", o.ID)
			}
			if beats[i].Group != nil {
				return Objective{}, fmt.Errorf("objective %s: a group gate belongs on a kill step, not a beat", beats[i].ID)
			}
		}
		o.Beats = beats
		if err := finishStep(&o); err != nil {
			return Objective{}, err
		}
	case ObjectiveLevel:
		if o.Level < 1 || o.Level > 1000 {
			return Objective{}, fmt.Errorf("objective %s: level must be 1..1000", o.ID)
		}
		if o.Count != 0 || o.Group != nil || len(o.Kinds) > 0 {
			return Objective{}, fmt.Errorf("objective %s: level does not take count, kinds, or group", o.ID)
		}
		if err := finishStep(&o); err != nil {
			return Objective{}, err
		}
	case ObjectiveInstanceRun:
		if o.Count == 0 {
			o.Count = 1
		}
		if o.Count < 1 || o.Count > 100 {
			return Objective{}, fmt.Errorf("objective %s: count must be 1..100", o.ID)
		}
		if o.Level != 0 {
			return Objective{}, fmt.Errorf("objective %s: instance_run uses count, not level (put size on group)", o.ID)
		}
		kinds, err := normalizeKinds(o.ID, o.Kinds)
		if err != nil {
			return Objective{}, err
		}
		o.Kinds = kinds
		if o.Group != nil {
			g, err := normalizeRunGroup(o.ID, o.Group)
			if err != nil {
				return Objective{}, err
			}
			o.Group = g
		}
		if err := finishStep(&o); err != nil {
			return Objective{}, err
		}
	case ObjectiveAllOf, ObjectiveAnyOf:
		if o.Memory != "" {
			return Objective{}, fmt.Errorf("objective %s: memory is not used on %s", o.ID, o.Type)
		}
		if o.Group != nil {
			return Objective{}, fmt.Errorf("objective %s: group belongs on a kill step", o.ID)
		}
		if len(o.Beats) > 0 || o.WithinSec != 0 {
			return Objective{}, fmt.Errorf("objective %s: within_sec and beats belong on a sequence", o.ID)
		}
		if len(o.Children) < 2 {
			return Objective{}, fmt.Errorf("objective %s: %s needs at least 2 nested objectives", o.ID, o.Type)
		}
		kids, err := normalizeList(o.Children, allowLegacy, seen, depth+1, budget)
		if err != nil {
			return Objective{}, err
		}
		o.Children = kids
	case objectiveLegacy:
		if !allowLegacy {
			return Objective{}, fmt.Errorf("objective %s: type legacy is not authorable", o.ID)
		}
		o.Children = nil
		o.Beats = nil
	default:
		return Objective{}, fmt.Errorf("objective %s: type must be go, talk, item, kill, emote, sequence, level, or instance_run", o.ID)
	}
	return o, nil
}

// finishStep clears nested trees and applies memory. A group gate is valid on
// kill (exact level) and instance_run (minimum size).
func finishStep(o *Objective) error {
	if o.Group != nil && o.Type != ObjectiveKill && o.Type != ObjectiveInstanceRun {
		return fmt.Errorf("objective %s: group belongs on a kill or instance_run step", o.ID)
	}
	if o.Type != ObjectiveSequence && (len(o.Beats) > 0 || o.WithinSec != 0) {
		return fmt.Errorf("objective %s: within_sec and beats belong on a sequence", o.ID)
	}
	if len(o.Children) > 0 {
		return fmt.Errorf("objective %s: nested objectives are only for all_of and any_of", o.ID)
	}
	if err := applyMemory(o); err != nil {
		return err
	}
	return nil
}

func sequenceBeat(kind string) bool {
	switch kind {
	case ObjectiveGo, ObjectiveLocation, ObjectiveTalk, ObjectiveInteract, ObjectiveEmote, ObjectiveKill:
		return true
	default:
		return false
	}
}

func applyMemory(o *Objective) error {
	mem := o.Memory
	switch o.Type {
	case ObjectiveGo, ObjectiveLocation, ObjectiveItem, ObjectiveCollect:
		if mem == "" {
			if o.Type == ObjectiveItem || o.Type == ObjectiveCollect {
				mem = MemoryHold
			} else {
				mem = MemoryVisit
			}
		}
		if mem != MemoryVisit && mem != MemoryHold {
			return fmt.Errorf("objective %s: memory must be visit or hold", o.ID)
		}
	case ObjectiveTalk, ObjectiveInteract, ObjectiveEmote, ObjectiveKill, ObjectiveSequence, ObjectiveLevel, ObjectiveInstanceRun:
		if mem == "" {
			mem = MemoryVisit
		}
		if mem != MemoryVisit {
			return fmt.Errorf("objective %s: %s is always visit", o.ID, o.Type)
		}
	default:
		if mem != "" {
			return fmt.Errorf("objective %s: memory is only for steps", o.ID)
		}
	}
	o.Memory = mem
	return nil
}

func normalizeStepGroup(id string, g *StepGroup) (*StepGroup, error) {
	if g.Level < 1 || g.Level > 1000 {
		return nil, fmt.Errorf("objective %s: group.level must be 1..1000 and is exact", id)
	}
	if g.Size != 0 && (g.Size < 2 || g.Size > 40) {
		return nil, fmt.Errorf("objective %s: group.size must be 2..40 when set", id)
	}
	inst := strings.TrimSpace(g.Instance)
	if utf8.RuneCountInString(inst) > 80 {
		return nil, fmt.Errorf("objective %s: group.instance is longer than 80 characters", id)
	}
	return &StepGroup{Level: g.Level, Size: g.Size, Instance: inst}, nil
}

// normalizeRunGroup is the soft party gate on instance_run: at least size
// members (default 2). level must be omitted. instance optionally filters the
// dungeon/raid name.
func normalizeRunGroup(id string, g *StepGroup) (*StepGroup, error) {
	if g.Level != 0 {
		return nil, fmt.Errorf("objective %s: instance_run group uses size, not level", id)
	}
	size := g.Size
	if size == 0 {
		size = 2
	}
	if size < 2 || size > 40 {
		return nil, fmt.Errorf("objective %s: group.size must be 2..40", id)
	}
	inst := strings.TrimSpace(g.Instance)
	if utf8.RuneCountInString(inst) > 80 {
		return nil, fmt.Errorf("objective %s: group.instance is longer than 80 characters", id)
	}
	return &StepGroup{Size: size, Instance: inst}, nil
}

func normalizeKinds(id string, in []string) ([]string, error) {
	if len(in) == 0 {
		return []string{"party", "raid"}, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, k := range in {
		k = strings.ToLower(strings.TrimSpace(k))
		if k != "party" && k != "raid" {
			return nil, fmt.Errorf("objective %s: kinds must be party and/or raid", id)
		}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, k)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("objective %s: kinds must be party and/or raid", id)
	}
	return out, nil
}

func displayID(id string, depth int) string {
	if id != "" {
		return id
	}
	return fmt.Sprintf("#%d", depth)
}

func hasPlace(o *Objective) bool {
	return o.Zone != "" || o.Subzone != "" || o.MapID != nil || o.X != nil || o.Y != nil || o.Radius != nil
}

// NormalizeEmote accepts "salute", "/salute", or "SALUTE" and returns SALUTE.
func NormalizeEmote(token string) (string, error) {
	token = strings.TrimSpace(token)
	token = strings.TrimPrefix(token, "/")
	token = strings.ToUpper(strings.ReplaceAll(token, " ", "_"))
	if !emoteToken.MatchString(token) {
		return "", fmt.Errorf("emote must be a token such as SALUTE or /salute")
	}
	return token, nil
}

// NormalizeTimeWindow fills the default clock and checks the window.
// A nil window is valid and means "always".
func NormalizeTimeWindow(w *TimeWindow) (*TimeWindow, error) {
	if w == nil {
		return nil, nil
	}
	clock := strings.ToLower(strings.TrimSpace(w.Clock))
	if clock == "" {
		clock = ClockLocal
	}
	if clock != ClockGame && clock != ClockLocal && clock != ClockRealm {
		return nil, fmt.Errorf("when.clock must be game, local, or realm")
	}
	start := strings.TrimSpace(w.Start)
	end := strings.TrimSpace(w.End)
	if (start == "") != (end == "") {
		return nil, fmt.Errorf("when.start and when.end are set together")
	}
	if start != "" && (!hhmm.MatchString(start) || !hhmm.MatchString(end)) {
		return nil, fmt.Errorf("when.start and when.end are HH:MM (00:00–23:59)")
	}
	var starts, ends time.Time
	var err error
	if strings.TrimSpace(w.StartsAt) != "" {
		starts, err = time.Parse(time.RFC3339, strings.TrimSpace(w.StartsAt))
		if err != nil {
			return nil, fmt.Errorf("when.starts_at must be RFC3339")
		}
		starts = starts.UTC()
	}
	if strings.TrimSpace(w.EndsAt) != "" {
		ends, err = time.Parse(time.RFC3339, strings.TrimSpace(w.EndsAt))
		if err != nil {
			return nil, fmt.Errorf("when.ends_at must be RFC3339")
		}
		ends = ends.UTC()
	}
	if !starts.IsZero() && !ends.IsZero() && !ends.After(starts) {
		return nil, fmt.Errorf("when.ends_at must be after when.starts_at")
	}
	if clock == ClockGame && (!starts.IsZero() || !ends.IsZero()) {
		return nil, fmt.Errorf("when.clock game has no calendar; use start and end only")
	}
	if start == "" && starts.IsZero() && ends.IsZero() {
		return nil, fmt.Errorf("when needs a time-of-day window and/or starts_at/ends_at")
	}
	out := &TimeWindow{Clock: clock, Start: start, End: end}
	if !starts.IsZero() {
		out.StartsAt = starts.Format(time.RFC3339)
	}
	if !ends.IsZero() {
		out.EndsAt = ends.Format(time.RFC3339)
	}
	return out, nil
}

// NormalizeGroup checks a saga group rule. A nil rule means the saga is solo.
func NormalizeGroup(g *GroupRule) (*GroupRule, error) {
	if g == nil {
		return nil, nil
	}
	if g.Size < 1 || g.Size > 40 {
		return nil, fmt.Errorf("group.size must be 1..40")
	}
	if g.MinLevel != nil && (*g.MinLevel < 1 || *g.MinLevel > 1000) {
		return nil, fmt.Errorf("group.min_level must be 1..1000")
	}
	inst := strings.TrimSpace(g.Instance)
	if utf8.RuneCountInString(inst) > 80 {
		return nil, fmt.Errorf("group.instance is longer than 80 characters")
	}
	out := &GroupRule{Size: g.Size, Instance: inst}
	if g.MinLevel != nil {
		n := *g.MinLevel
		out.MinLevel = &n
	}
	return out, nil
}

// GroupSatisfied reports whether the reported roster meets the rule.
// A nil rule is always satisfied. Members below min_level do not count.
// instance is the place the addon matched (instance name or zone).
func GroupSatisfied(rule *GroupRule, members []GroupMember, instance string) error {
	if rule == nil {
		return nil
	}
	minLevel := 1
	if rule.MinLevel != nil {
		minLevel = *rule.MinLevel
	}
	n := 0
	seen := map[string]bool{}
	for _, m := range members {
		name := strings.TrimSpace(m.Name)
		if name == "" || m.Level < minLevel {
			continue
		}
		key := strings.ToLower(name) + "\n" + strings.ToLower(strings.TrimSpace(m.Realm))
		if seen[key] {
			continue
		}
		seen[key] = true
		n++
	}
	if n < rule.Size {
		return fmt.Errorf("group needs %d members at level %d or higher", rule.Size, minLevel)
	}
	if rule.Instance != "" && !NameListed(rule.Instance, instance) {
		return fmt.Errorf("group instance does not match")
	}
	return nil
}

// EnsureMember appends name/realm/level when that character is not already listed.
func EnsureMember(members []GroupMember, name, realm string, level int) []GroupMember {
	name = strings.TrimSpace(name)
	realm = strings.TrimSpace(realm)
	if name == "" {
		return members
	}
	for i := range members {
		if strings.EqualFold(strings.TrimSpace(members[i].Name), name) &&
			(realm == "" || members[i].Realm == "" || strings.EqualFold(strings.TrimSpace(members[i].Realm), realm)) {
			if members[i].Level < level {
				members[i].Level = level
			}
			if members[i].Realm == "" {
				members[i].Realm = realm
			}
			return members
		}
	}
	return append(members, GroupMember{Name: name, Realm: realm, Level: level})
}

// NameListed is the API side of the addon's name match: "|" or ";" split
// alternatives, ignoring case and punctuation, with either side containing the other.
func NameListed(want, got string) bool {
	gotKey := nameKey(got)
	if strings.TrimSpace(want) == "" || gotKey == "" {
		return false
	}
	if nameKey(want) == gotKey {
		return true
	}
	for _, part := range splitAlts(want) {
		partKey := nameKey(part)
		if partKey == "" {
			continue
		}
		if partKey == gotKey || strings.Contains(gotKey, partKey) || strings.Contains(partKey, gotKey) {
			return true
		}
	}
	return false
}

func splitAlts(want string) []string {
	return strings.FieldsFunc(want, func(r rune) bool { return r == '|' || r == ';' })
}

func nameKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func normalizeLocation(o *Objective) error {
	hasZone := o.Zone != "" || o.Subzone != "" || (o.MapID != nil && *o.MapID > 0)
	hasPoint := o.X != nil || o.Y != nil
	if o.MapID != nil && *o.MapID <= 0 {
		return fmt.Errorf("objective %s: map_id must be positive", o.ID)
	}
	if hasPoint && (o.X == nil || o.Y == nil) {
		return fmt.Errorf("objective %s: x and y are set together", o.ID)
	}
	if hasPoint {
		if *o.X < 0 || *o.X > 1 || *o.Y < 0 || *o.Y > 1 {
			return fmt.Errorf("objective %s: x and y are map fractions from 0 to 1", o.ID)
		}
		if o.Radius == nil {
			r := DefaultRadius
			o.Radius = &r
		}
		if *o.Radius <= 0 || *o.Radius > 1 {
			return fmt.Errorf("objective %s: radius must be within (0, 1]", o.ID)
		}
	} else if o.Radius != nil {
		return fmt.Errorf("objective %s: radius requires x and y", o.ID)
	}
	if !hasZone && !hasPoint {
		return fmt.Errorf("objective %s: location needs zone, subzone, map_id, or x/y", o.ID)
	}
	return nil
}

func clipField(values ...string) error {
	for _, v := range values {
		if utf8.RuneCountInString(v) > 80 {
			return fmt.Errorf("field longer than 80 characters")
		}
	}
	return nil
}
