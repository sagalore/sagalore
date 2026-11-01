package syncdata

import (
	"strings"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func intptr(n int) *int { return &n }

func TestMergeRules(t *testing.T) {
	min10 := 10
	min40 := 40
	sync := contract.SyncResponse{
		Characters: []contract.Character{
			{ID: "c1", Name: "Magni", Realm: "Argent Dawn", Level: 12},
			{ID: "c2", Name: "Mira", Realm: "Argent Dawn", Level: 8},
		},
		Quests: []contract.Quest{
			{ID: "q-party", Title: "Lagoppdrag", Active: true, PartyQuest: true, Version: 2, MinLevel: &min10, AssignedTo: []contract.IDRef{}},
			{ID: "q-mine", Title: "Bare Magni", Active: true, Version: 4, MinLevel: intptr(1), AssignedTo: []contract.IDRef{{ID: "c1"}}},
			{ID: "q-high", Title: "For høyt", Active: true, PartyQuest: true, Version: 1, MinLevel: &min40},
			{ID: "q-off", Title: "Av", Active: false, PartyQuest: true, Version: 3},
			{ID: "q-done", Title: "Gammelt", Active: false, Version: 5, AssignedTo: []contract.IDRef{{ID: "c1"}}},
		},
		Progress: []contract.Progress{
			{QuestID: "q-done", CharacterID: "c1", Status: "completed", QuestVersion: 5, CompletedAt: "2026-10-01T00:00:00Z"},
		},
	}
	entries := Merge(sync)
	got := map[string]Entry{}
	for _, e := range entries {
		got[e.Character+"/"+e.QuestID] = e
	}
	if _, ok := got["Magni/q-party"]; !ok {
		t.Fatal("party quest should show for Magni")
	}
	if _, ok := got["Mira/q-party"]; !ok {
		t.Fatal("min_level is not a hard gate; Mira should still see the party quest")
	}
	if e := got["Magni/q-mine"]; e.QuestVersion != 4 {
		t.Fatalf("version %d", e.QuestVersion)
	}
	if _, ok := got["Mira/q-mine"]; ok {
		t.Fatal("assigned only to Magni")
	}
	if _, ok := got["Magni/q-high"]; !ok {
		t.Fatal("high min_level should still appear (colored in the addon)")
	}
	if _, ok := got["Magni/q-off"]; ok {
		t.Fatal("inactive")
	}
	if e := got["Magni/q-done"]; e.Status != "completed" {
		t.Fatal("completed inactive quest should remain")
	}
	if !strings.Contains(WriteLua("Magnus#2231", "2026-10-22T00:00:00Z", entries), `saga_version = 4`) {
		t.Fatal(WriteLua("Magnus#2231", "2026-10-22T00:00:00Z", entries))
	}
}

func TestSagaAuthorAndCompleters(t *testing.T) {
	sync := contract.SyncResponse{
		Characters: []contract.Character{
			{ID: "c1", Name: "Magni", Realm: "Celebras", Level: 1},
		},
		Quests: []contract.Quest{
			{ID: "q1", Title: "En kveld", Active: true, PartyQuest: true, Version: 1, CreatedBy: "Sagalore#0000"},
		},
		Progress: []contract.Progress{
			{QuestID: "q1", CharacterID: "c1", Status: "completed", CompletedAt: "2026-10-22T17:00:00Z"},
		},
		Completions: []contract.QuestCompletion{
			{QuestID: "q1", Character: "Mira", Realm: "Celebras", BattleTag: "Mira#1234", Level: 8, Race: "Human", Class: "Mage", CompletedAt: "2026-10-22T18:00:00Z"},
			{QuestID: "q1", Character: "Magni", Realm: "Celebras", BattleTag: "Magnus#2231", Level: 12, Race: "Dwarf", Class: "Warrior", CompletedAt: "2026-10-22T17:00:00Z"},
		},
	}
	entries := Merge(sync)
	if len(entries) != 1 || entries[0].CreatedBy != "Sagalore#0000" || len(entries[0].CompletedBy) != 2 {
		t.Fatalf("%+v", entries)
	}
	if entries[0].CompletedBy[0].Character != "Magni" || entries[0].CompletedBy[1].Character != "Mira" {
		t.Fatalf("expected chronological completers, got %+v", entries[0].CompletedBy)
	}
	if entries[0].CompletedBy[0].BattleTag != "Magnus#2231" || entries[0].CompletedBy[0].Class != "Warrior" {
		t.Fatalf("identity %+v", entries[0].CompletedBy[0])
	}
	if entries[0].CompletedBy[0].When != "22 October 2026 19:00" {
		t.Fatalf("when %+v", entries[0].CompletedBy[0])
	}
	lua := WriteLua("", "t", entries)
	if !strings.Contains(lua, `created_by = "Sagalore#0000"`) || !strings.Contains(lua, `character = "Mira"`) ||
		!strings.Contains(lua, `battletag = "Magnus#2231"`) || !strings.Contains(lua, `class = "Warrior"`) ||
		!strings.Contains(lua, `when = "22 October 2026 19:00"`) {
		t.Fatal(lua)
	}
}

func TestLuaEscapes(t *testing.T) {
	lua := WriteLua(`Magnus#2231`, "t", []Entry{{
		Character: "Magni", Title: `Si "hei"`, Description: "linje\nto", QuestVersion: 1, QuestID: "q",
	}})
	if !strings.Contains(lua, `Si \"hei\"`) || !strings.Contains(lua, `linje\nto`) {
		t.Fatal(lua)
	}
}

func TestChainLockFollowsAnyCompletionVersion(t *testing.T) {
	sync := contract.SyncResponse{
		Characters: []contract.Character{
			{ID: "c1", Name: "Magni", Realm: "Celebras", Level: 10},
		},
		Quests: []contract.Quest{
			{ID: "q1", Title: "Kapittel 1", Active: true, PartyQuest: true, Version: 4, Chain: &contract.Chain{ID: "goldshire", Name: "Goldshire", Order: 1, Index: 1, Total: 2}},
			{ID: "q2", Title: "Kapittel 2", Active: true, PartyQuest: true, Version: 1, Requires: []string{"q1"}, Chain: &contract.Chain{ID: "goldshire", Name: "Goldshire", Order: 2, Index: 2, Total: 2}},
		},
		Progress: []contract.Progress{
			{QuestID: "q1", CharacterID: "c1", Status: "completed", QuestVersion: 1},
		},
	}
	entries := Merge(sync)
	byID := map[string]Entry{}
	for _, e := range entries {
		byID[e.QuestID] = e
	}
	if len(byID) != 2 || byID["q2"].Locked || byID["q2"].ChainIndex != 2 || byID["q2"].ChainTotal != 2 {
		t.Fatalf("older completion should unlock the next saga: %+v", entries)
	}
	if len(byID["q2"].Requires) != 1 || byID["q2"].Requires[0].Title != "Kapittel 1" {
		t.Fatalf("%+v", byID["q2"])
	}
	if entries[0].QuestID != "q2" || entries[1].Status != "completed" {
		t.Fatalf("open chapter stays ahead of the finished one: %+v", entries)
	}
	lua := WriteLua("", "t", entries)
	if !strings.Contains(lua, `chain_index = 2`) || !strings.Contains(lua, `title = "Kapittel 1"`) || !strings.Contains(lua, `locked = false`) {
		t.Fatal(lua)
	}
	sync.Progress = nil
	locked := Merge(sync)
	if len(locked) != 2 || locked[0].QuestID != "q1" || locked[0].Locked || !locked[1].Locked {
		t.Fatalf("%+v", locked)
	}
}

func TestFindCharacter(t *testing.T) {
	sync := contract.SyncResponse{Characters: []contract.Character{
		{ID: "1", Name: "Magni", Realm: "Argent Dawn"},
		{ID: "2", Name: "Magni", Realm: "Silvermoon"},
	}}
	if _, ok := FindCharacter(sync, "magni", ""); ok {
		t.Fatal("ambiguous without realm")
	}
	c, ok := FindCharacter(sync, "Magni", "argent dawn")
	if !ok || c.ID != "1" {
		t.Fatalf("%+v %v", c, ok)
	}
}

func TestDurationAndLeaderboardLua(t *testing.T) {
	fast := 90
	sync := contract.SyncResponse{
		Characters: []contract.Character{{ID: "c1", Name: "Magni", Realm: "Celebras", Level: 12}},
		Quests:     []contract.Quest{{ID: "q1", Title: "Rabbits", Active: true, PartyQuest: true, Version: 1}},
		Completions: []contract.QuestCompletion{{
			QuestID: "q1", Character: "Magni", Realm: "Celebras", Level: 12,
			CompletedAt: "2026-10-29T12:00:00Z", StartedAt: "2026-10-29T11:58:30Z", DurationSec: &fast,
		}},
		Leaderboards: []contract.Leaderboard{{
			GroupID: "g1", GroupName: "Tutorial", Period: "week",
			Entries: []contract.LeaderboardEntry{{
				Rank: 1, Name: "Magni", Realm: "Celebras", Class: "Warrior", Race: "Dwarf",
				Level: 12, Completed: 1, LastCompletedAt: "2026-10-29T12:00:00Z",
			}},
		}},
		Groups: []contract.GroupSummary{{ID: "g1", Slug: "tutorial", Name: "Tutorial"}},
	}
	entries := Merge(sync)
	if len(entries) != 1 || len(entries[0].CompletedBy) != 1 || entries[0].CompletedBy[0].DurationSec == nil || *entries[0].CompletedBy[0].DurationSec != 90 {
		t.Fatalf("%+v", entries)
	}
	lua := WriteSyncLua(sync.BattleTag, "t", entries, "", "", sync.Groups, nil, sync.Characters, sync.Leaderboards)
	if !strings.Contains(lua, "duration_sec = 90") || !strings.Contains(lua, `period = "week"`) || !strings.Contains(lua, `name = "Magni"`) {
		t.Fatal(lua)
	}
}

func TestMergeProvisionalCharacterWhenNoneReported(t *testing.T) {
	sync := contract.SyncResponse{
		Quests: []contract.Quest{
			{ID: "q1", Title: "Say hello", Active: true, PartyQuest: true, Version: 1},
		},
	}
	entries := Merge(sync)
	if len(entries) != 1 || entries[0].Character != "*" || entries[0].QuestID != "q1" {
		t.Fatalf("%+v", entries)
	}
}
