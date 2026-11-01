package contract

import "testing"

func TestNormalizeStepContract(t *testing.T) {
	level := 17
	in := []Objective{
		{ID: "inn", Type: "go", Text: "Stå i Goldshire", Memory: "hold", Zone: "Elwynn Forest"},
		{ID: "farley", Type: "talk", Text: "Snakk med Farley", NPC: "Innkeeper Farley"},
		{ID: "water", Type: "item", Text: "Ha vann", ItemID: &level},
		{
			ID: "trick", Type: "sequence", Text: "Klapp, så frekk", WithinSec: 30,
			Beats: []Objective{
				{ID: "clap", Type: "emote", Text: "Klapp", Emote: "/applaud", NPC: "Brother Wilhelm"},
				{ID: "rude", Type: "emote", Text: "Frekk", Emote: "rude", NPC: "Innkeeper Farley"},
			},
		},
		{
			ID: "boss", Type: "kill", Text: "VanCleef", NPC: "Edwin VanCleef",
			Group: &StepGroup{Level: 17, Instance: "The Deadmines|Deadmines"},
		},
	}
	// item_id 17 is only a positive int for this shape test.
	out, err := NormalizeObjectives(in, false)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Memory != MemoryHold || out[1].Memory != MemoryVisit || out[2].Memory != MemoryHold {
		t.Fatalf("memory defaults: %+v", []string{out[0].Memory, out[1].Memory, out[2].Memory})
	}
	if out[2].Count != 1 {
		t.Fatalf("item count defaulted to %d", out[2].Count)
	}
	if out[3].Beats[0].Emote != "APPLAUD" || out[3].Beats[1].Emote != "RUDE" {
		t.Fatalf("beats: %+v", out[3].Beats)
	}
	if out[4].Group == nil || out[4].Group.Level != 17 || out[4].Memory != MemoryVisit {
		t.Fatalf("group kill: %+v", out[4])
	}
}

func TestNormalizeRejectsLooseSteps(t *testing.T) {
	cases := []Objective{
		{ID: "bad", Type: "talk", Text: "Snakk", NPC: "Farley", Memory: "hold"},
		{ID: "or", Type: "emote", Text: "Ett av dem", Emote: "BOW", Group: &StepGroup{Level: 17}},
		{ID: "short", Type: "sequence", Text: "For kort", WithinSec: 30, Beats: []Objective{
			{ID: "only", Type: "emote", Text: "En", Emote: "WAVE"},
		}},
		{ID: "boss", Type: "kill", Text: "Boss", NPC: "Edwin VanCleef", Group: &StepGroup{Level: 0}},
	}
	for _, o := range cases {
		if _, err := NormalizeObjectives([]Objective{o}, false); err == nil {
			t.Fatalf("accepted %s", o.ID)
		}
	}
}

func TestNormalizeLevelAndInstanceRun(t *testing.T) {
	out, err := NormalizeObjectives([]Objective{
		{ID: "ten", Type: "level", Text: "Bli level 10", Level: 10,
			When: &TimeWindow{Clock: "local", EndsAt: "2026-11-20T23:59:59+01:00"}},
		{ID: "runs", Type: "instance_run", Text: "Fem runs", Count: 5, Group: &StepGroup{Size: 2}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Memory != MemoryVisit || out[0].Level != 10 || out[0].When == nil {
		t.Fatalf("level: %+v", out[0])
	}
	if out[1].Count != 5 || len(out[1].Kinds) != 2 || out[1].Group == nil || out[1].Group.Size != 2 || out[1].Group.Level != 0 {
		t.Fatalf("instance_run: %+v", out[1])
	}
	if _, err := NormalizeObjectives([]Objective{
		{ID: "bad", Type: "level", Text: "X", Level: 10, Group: &StepGroup{Size: 2}},
	}, false); err == nil {
		t.Fatal("level must reject group")
	}
	if _, err := NormalizeObjectives([]Objective{
		{ID: "bad", Type: "instance_run", Text: "X", Group: &StepGroup{Level: 17, Size: 2}},
	}, false); err == nil {
		t.Fatal("instance_run must reject group.level")
	}
}

func TestLegacyObjectivesStillNormalize(t *testing.T) {
	item := 159
	out, err := NormalizeObjectives([]Objective{
		{ID: "inn", Type: "location", Text: "Goldshire", Zone: "Elwynn Forest"},
		{ID: "both", Type: "all_of", Text: "Begge", Children: []Objective{
			{ID: "here", Type: "location", Text: "Her", Zone: "Elwynn Forest"},
			{ID: "water", Type: "collect", Text: "Vann", ItemID: &item},
		}},
	}, false)
	if err != nil {
		t.Fatal(err)
	}
	if out[0].Type != ObjectiveLocation || out[0].Memory != MemoryVisit {
		t.Fatalf("location: %+v", out[0])
	}
	if out[1].Children[1].Memory != MemoryHold {
		t.Fatalf("collect defaults to hold: %+v", out[1].Children[1])
	}
}
