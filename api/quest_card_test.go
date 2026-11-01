package main

import (
	"strings"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestBuildGoalsMarkdown(t *testing.T) {
	q := contract.Quest{
		Objectives: []contract.Objective{
			{ID: "a", Type: "talk", Text: "Snakk med Farley"},
			{ID: "b", Type: "go", Text: "Stå i Goldshire"},
		},
	}
	got := buildGoalsMarkdown(q)
	if got != "1. Snakk med Farley\n2. Stå i Goldshire" {
		t.Fatalf("goals %q", got)
	}
	if buildGoalsMarkdown(contract.Quest{}) != "(no goals)" {
		t.Fatal("empty")
	}
}

func TestBuildActivityCard(t *testing.T) {
	card := buildActivityCard(nil)
	if card == nil || len(card.Columns) != 3 || len(card.Rows) != 1 {
		t.Fatalf("empty %+v", card)
	}
	if card.Rows[0].Cells["character"] != "No one has completed this yet" || card.Rows[0].Group != "Activity" {
		t.Fatalf("empty row %+v", card.Rows[0])
	}

	card = buildActivityCard([]contract.QuestCompleter{{
		Character: "Magni", Class: "Paladin", Level: 12, Realm: "Celebras", When: "22 October 2026 19:00",
	}})
	if len(card.Rows) != 1 {
		t.Fatalf("rows %d", len(card.Rows))
	}
	if card.Rows[0].Group != "Activity" {
		t.Fatalf("group %q", card.Rows[0].Group)
	}
	if card.Rows[0].Cells["character"] != "Magni completed" {
		t.Fatalf("character %v", card.Rows[0].Cells["character"])
	}
	detail, _ := card.Rows[0].Cells["detail"].(string)
	if !strings.Contains(detail, "Paladin") || !strings.Contains(detail, "Celebras") {
		t.Fatalf("detail %q", detail)
	}
	if card.Rows[0].Cells["when"] != "22 October 2026 19:00" {
		t.Fatalf("when %v", card.Rows[0].Cells["when"])
	}
}
