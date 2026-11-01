package syncdata

import (
	"sort"
	"strings"

	"github.com/sagalore/sagalore/internal/contract"
)

// Entry is one quest as a character should see it in the addon.
type Entry struct {
	Character         string               `json:"character"`
	CharacterID       string               `json:"character_id"`
	Realm             string               `json:"realm"`
	QuestID           string               `json:"saga_id"`
	QuestVersion      int                  `json:"saga_version"`
	Title             string               `json:"title"`
	Description       string               `json:"description"`
	Objectives        []contract.Objective `json:"objectives"`
	ObjectivesOrdered bool                 `json:"objectives_ordered"`
	When              *contract.TimeWindow `json:"when,omitempty"`
	Group             *contract.GroupRule  `json:"group,omitempty"`
	GroupID           string               `json:"group_id,omitempty"`
	GroupSlug         string               `json:"group_slug,omitempty"`
	GroupName         string               `json:"group_name,omitempty"`
	PendingInviteID   string               `json:"pending_invite_id,omitempty"`
	MinLevel          *int                 `json:"min_level"`
	Status            string               `json:"status"`
	CompletedAt       string               `json:"completed_at"`
	PartyQuest        bool                 `json:"party"`
	CreatedBy         string               `json:"created_by"`
	CompletedBy       []Completer          `json:"completed_by"`
	Requires          []contract.Require   `json:"requires,omitempty"`
	ChainID           string               `json:"chain_id,omitempty"`
	ChainName         string               `json:"chain_name,omitempty"`
	ChainOrder        int                  `json:"chain_order,omitempty"`
	ChainIndex        int                  `json:"chain_index,omitempty"`
	ChainTotal        int                  `json:"chain_total,omitempty"`
	Locked            bool                 `json:"locked,omitempty"`
	Image             string               `json:"image,omitempty"`        // Interface\\AddOns\\Sagalore\\media\\<id>
	ImageAspect       float64              `json:"image_aspect,omitempty"` // width/height
	HasImage          bool                 `json:"has_image"`
}

// Completer is a character who has finished this saga.
type Completer struct {
	Character   string `json:"character"`
	Realm       string `json:"realm"`
	BattleTag   string `json:"battletag"`
	DisplayName string `json:"display_name,omitempty"`
	Level       int    `json:"level,omitempty"`
	Race        string `json:"race,omitempty"`
	Class       string `json:"class,omitempty"`
	CompletedAt string `json:"completed_at"`
	StartedAt   string `json:"started_at,omitempty"`
	DurationSec *int   `json:"duration_sec,omitempty"`
	When        string `json:"when,omitempty"` // "dd mmmm yyyy hh:mm" Europe/Oslo
}

// Merge applies the companion rule: a character sees an active quest when it
// is a party quest or assigned to them. min_level is advice for the addon
// (difficulty color), not a filter. A completed quest stays visible even if
// it later becomes inactive or the character would no longer be eligible.
func Merge(sync contract.SyncResponse) []Entry {
	progress := map[string]contract.Progress{}
	doneBy := map[string]map[string]bool{}
	for _, p := range sync.Progress {
		progress[p.CharacterID+"\n"+p.QuestID] = p
		if p.Status != "completed" {
			continue
		}
		if doneBy[p.CharacterID] == nil {
			doneBy[p.CharacterID] = map[string]bool{}
		}
		doneBy[p.CharacterID][p.QuestID] = true
	}
	titles := map[string]string{}
	for _, q := range sync.Quests {
		titles[q.ID] = q.Title
	}
	completers := completersByQuest(sync)
	chars := sync.Characters
	// Before the addon has reported a character (common right after Battle.net
	// login on Classic Era), still emit party sagas so Personal starters show
	// on the first /reload. The addon treats character "*" as "any toon".
	if len(chars) == 0 && len(sync.Quests) > 0 {
		chars = []contract.Character{{Name: "*", Level: 1000, Participating: true}}
	}
	var out []Entry
	for _, ch := range chars {
		for _, q := range sync.Quests {
			p, has := progress[ch.ID+"\n"+q.ID]
			completed := has && p.Status == "completed"
			if !contract.Eligible(ch.Level, ch.ID, q) && !completed {
				continue
			}
			locked, _ := contract.Gate(q.ID, q.Requires, doneBy[ch.ID])
			e := Entry{
				Character: ch.Name, CharacterID: ch.ID, Realm: ch.Realm,
				QuestID: q.ID, QuestVersion: q.Version, Title: q.Title,
				Description: q.Description, Objectives: q.Objectives,
				ObjectivesOrdered: q.ObjectivesOrdered, When: q.When, Group: q.Group,
				GroupID: q.GroupID, GroupSlug: q.GroupSlug, GroupName: q.GroupName,
				PendingInviteID: q.PendingInviteID,
				MinLevel: q.MinLevel, PartyQuest: q.PartyQuest,
				CreatedBy: q.CreatedBy, CompletedBy: completers[q.ID],
				Requires: requireRefs(q.Requires, titles), Locked: locked,
				HasImage: q.HasImage,
			}
			if q.Chain != nil && q.Chain.ID != "" {
				e.ChainID = q.Chain.ID
				e.ChainName = q.Chain.Name
				e.ChainOrder = q.Chain.Order
				e.ChainIndex = q.Chain.Index
				e.ChainTotal = q.Chain.Total
			}
			if q.HasImage {
				e.Image = `Interface\AddOns\Sagalore\media\` + q.ID
			}
			if has {
				e.Status = p.Status
				e.CompletedAt = p.CompletedAt
				if completed && p.QuestVersion > 0 {
					e.QuestVersion = p.QuestVersion
				}
			}
			out = append(out, e)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Character != out[j].Character {
			return out[i].Character < out[j].Character
		}
		ic := out[i].Status == "completed"
		jc := out[j].Status == "completed"
		if ic != jc {
			return !ic
		}
		if (out[i].ChainID == "") != (out[j].ChainID == "") {
			return out[i].ChainID != ""
		}
		if out[i].ChainName != out[j].ChainName {
			return out[i].ChainName < out[j].ChainName
		}
		if out[i].ChainOrder != out[j].ChainOrder {
			return out[i].ChainOrder < out[j].ChainOrder
		}
		li, lj := 0, 0
		if out[i].MinLevel != nil {
			li = *out[i].MinLevel
		}
		if out[j].MinLevel != nil {
			lj = *out[j].MinLevel
		}
		if li != lj {
			return li < lj
		}
		return out[i].Title < out[j].Title
	})
	return out
}

func requireRefs(ids []string, titles map[string]string) []contract.Require {
	if len(ids) == 0 {
		return nil
	}
	out := make([]contract.Require, 0, len(ids))
	for _, id := range ids {
		out = append(out, contract.Require{ID: id, Title: titles[id]})
	}
	return out
}

func completersByQuest(sync contract.SyncResponse) map[string][]Completer {
	type key struct{ quest, name, realm string }
	seen := map[key]bool{}
	out := map[string][]Completer{}
	add := func(questID, name, realm, tag, displayName, race, class string, level int, at, started string, duration *int) {
		name = strings.TrimSpace(name)
		realm = strings.TrimSpace(realm)
		tag = strings.TrimSpace(tag)
		displayName = strings.TrimSpace(displayName)
		race = strings.TrimSpace(race)
		class = strings.TrimSpace(class)
		if questID == "" || name == "" {
			return
		}
		k := key{questID, strings.ToLower(name), strings.ToLower(realm)}
		if seen[k] {
			list := out[questID]
			for i := range list {
				if !strings.EqualFold(list[i].Character, name) || !strings.EqualFold(list[i].Realm, realm) {
					continue
				}
				if list[i].BattleTag == "" && tag != "" {
					list[i].BattleTag = tag
				}
				if list[i].DisplayName == "" && displayName != "" {
					list[i].DisplayName = displayName
				}
				if list[i].Race == "" && race != "" {
					list[i].Race = race
				}
				if list[i].Class == "" && class != "" {
					list[i].Class = class
				}
				if list[i].Level < 1 && level > 0 {
					list[i].Level = level
				}
				if list[i].CompletedAt == "" && at != "" {
					list[i].CompletedAt = at
					list[i].When = FormatOsloWhen(at)
				}
				if list[i].StartedAt == "" && started != "" {
					list[i].StartedAt = started
				}
				if list[i].DurationSec == nil && duration != nil {
					n := *duration
					list[i].DurationSec = &n
				}
				break
			}
			return
		}
		seen[k] = true
		var copied *int
		if duration != nil {
			n := *duration
			copied = &n
		}
		out[questID] = append(out[questID], Completer{
			Character: name, Realm: realm, BattleTag: tag,
			DisplayName: displayName, Level: level, Race: race, Class: class,
			CompletedAt: at, StartedAt: started, DurationSec: copied, When: FormatOsloWhen(at),
		})
	}
	known := map[string]contract.Character{}
	for _, ch := range sync.Characters {
		known[ch.ID] = ch
	}
	for _, p := range sync.Progress {
		if p.Status != "completed" {
			continue
		}
		ch := known[p.CharacterID]
		add(p.QuestID, ch.Name, ch.Realm, ch.BattleTag, "", ch.Race, ch.Class, ch.Level, p.CompletedAt, p.StartedAt, p.DurationSec)
	}
	for _, c := range sync.Completions {
		add(c.QuestID, c.Character, c.Realm, c.BattleTag, c.DisplayName, c.Race, c.Class, c.Level, c.CompletedAt, c.StartedAt, c.DurationSec)
	}
	for id := range out {
		sort.SliceStable(out[id], func(i, j int) bool {
			return out[id][i].CompletedAt < out[id][j].CompletedAt
		})
	}
	return out
}

// FindCharacter matches name and, when both sides have a realm, the realm.
func FindCharacter(sync contract.SyncResponse, name, realm string) (contract.Character, bool) {
	var matches []contract.Character
	for _, c := range sync.Characters {
		if !strings.EqualFold(c.Name, name) {
			continue
		}
		if realm != "" && c.Realm != "" && !strings.EqualFold(c.Realm, realm) {
			continue
		}
		matches = append(matches, c)
	}
	if len(matches) == 1 {
		return matches[0], true
	}
	return contract.Character{}, false
}

func QuestByID(sync contract.SyncResponse, id string) (contract.Quest, bool) {
	for _, q := range sync.Quests {
		if q.ID == id {
			return q, true
		}
	}
	return contract.Quest{}, false
}
