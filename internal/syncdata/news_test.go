package syncdata

import (
	"strings"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func baseSync() contract.SyncResponse {
	return contract.SyncResponse{
		GeneratedAt: "t0",
		BattleTag:   "Magnus#2231",
		Characters: []contract.Character{{
			ID: "c1", Name: "Magni", Realm: "Argent Dawn", Level: 20,
		}},
	}
}

func party(id, title string, version int, active bool) contract.Quest {
	return contract.Quest{
		ID: id, Title: title, Version: version, Active: active, PartyQuest: true,
		Objectives: []contract.Objective{{ID: "a", Type: "location", Text: "Stå"}},
	}
}

func TestReloadNews(t *testing.T) {
	prev := baseSync()
	next := baseSync()
	next.GeneratedAt = "t1"
	next.Quests = []contract.Quest{party("q1", "En kveld i Goldshire", 1, true)}
	if d, _ := ReloadNews(contract.SyncResponse{}, next); d != "" {
		t.Fatalf("startup must stay quiet, got %q", d)
	}
	d, a := ReloadNews(prev, next)
	if d != "New saga: En kveld i Goldshire — type /reload in WoW" {
		t.Fatal(d)
	}
	if a != "New saga: En kveld i Goldshire is loaded." {
		t.Fatal(a)
	}
	same := next
	same.GeneratedAt = "t2"
	if d, _ := ReloadNews(next, same); d != "" {
		t.Fatalf("unchanged sync notified: %q", d)
	}

	two := next
	two.Quests = []contract.Quest{
		party("q1", "En kveld i Goldshire", 1, true),
		party("q2", "Melk til vaktlaget", 1, true),
		party("q3", "Veien fra Northshire", 1, true),
	}
	d, _ = ReloadNews(next, two)
	if d != "New saga: Melk til vaktlaget and 1 more — type /reload in WoW" {
		t.Fatal(d)
	}

	edited := two
	edited.Quests = []contract.Quest{
		party("q1", "En kveld i Goldshire", 2, true),
		party("q2", "Melk til vaktlaget", 1, true),
		party("q3", "Veien fra Northshire", 1, true),
	}
	d, _ = ReloadNews(two, edited)
	if d != "Saga updated: En kveld i Goldshire — type /reload in WoW" {
		t.Fatal(d)
	}

	retired := edited
	retired.Quests = []contract.Quest{
		party("q1", "En kveld i Goldshire", 3, false),
		party("q2", "Melk til vaktlaget", 1, true),
		party("q3", "Veien fra Northshire", 1, true),
	}
	d, _ = ReloadNews(edited, retired)
	if d != "Saga retired: En kveld i Goldshire — type /reload in WoW" {
		t.Fatal(d)
	}

	done := retired
	done.Progress = []contract.Progress{{
		QuestID: "q2", CharacterID: "c1", QuestVersion: 1, Status: "completed",
	}}
	done.Completions = []contract.QuestCompletion{{
		QuestID: "q2", Character: "Magni", Realm: "Argent Dawn",
	}}
	d, a = ReloadNews(retired, done)
	if d != "Saga completed: Melk til vaktlaget — type /reload in WoW" {
		t.Fatal(d)
	}
	if !strings.Contains(a, "is loaded") {
		t.Fatal(a)
	}

	// Leveling past min_level is not a sync event — sagas stay visible and
	// the addon only recolors the title.
	low := baseSync()
	low.Characters[0].Level = 5
	low.Quests = []contract.Quest{{
		ID: "q9", Title: "Vaktpost", Version: 1, Active: true, PartyQuest: true, MinLevel: intPtr(10),
	}}
	high := low
	high.Characters = []contract.Character{{ID: "c1", Name: "Magni", Realm: "Argent Dawn", Level: 10}}
	high.Quests = low.Quests
	d, _ = ReloadNews(low, high)
	if d != "" {
		t.Fatalf("level-only change should be quiet, got %q", d)
	}

	mira := next
	mira.Characters = append(append([]contract.Character{}, next.Characters...), contract.Character{
		ID: "c2", Name: "Mira", Realm: "Argent Dawn", Level: 20,
	})
	d, _ = ReloadNews(next, mira)
	if d != "Saga updated: En kveld i Goldshire — type /reload in WoW" {
		t.Fatal(d)
	}
}

func TestLuaNotice(t *testing.T) {
	lua := WriteLuaFile("Magnus#2231", "t", nil, `New saga: Say "hi" is loaded.`, "id-1")
	if !strings.Contains(lua, `notice = "New saga: Say \"hi\" is loaded."`) || !strings.Contains(lua, `notice_id = "id-1"`) || !strings.Contains(lua, "SagaloreData") {
		t.Fatal(lua)
	}
	if strings.Contains(WriteLua("Magnus#2231", "t", nil), "notice") {
		t.Fatal("quiet file should omit notice")
	}
}

func intPtr(n int) *int { return &n }
