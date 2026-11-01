package main

import (
	"encoding/base64"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func encodePNG(raw []byte) string {
	return base64.StdEncoding.EncodeToString(raw)
}

func TestAnnounceFlags(t *testing.T) {
	no := false
	yes := true
	if !announceCreate(nil, true) || announceCreate(nil, false) {
		t.Fatal("default follows active")
	}
	if announceCreate(&no, true) || !announceCreate(&yes, false) {
		t.Fatal("explicit flag")
	}
	if !announceUpdate(nil) || announceUpdate(&no) || !announceUpdate(&yes) {
		t.Fatal("update flag")
	}
}

func TestSignificantQuestChangeIgnoresAssignment(t *testing.T) {
	base := contract.Quest{
		Title: "Vakt", Description: "Stå der.",
		Objectives: []contract.Objective{{ID: "a", Type: "interact", Text: "Møt opp", NPC: "Vakt"}},
	}
	same := base
	same.AssignedTo = []contract.IDRef{{ID: "11111111-1111-1111-1111-111111111111"}}
	if significantQuestChange(base, same) {
		t.Fatal("assignment is not a significant change")
	}
	renamed := base
	renamed.Title = "Ny vakt"
	if !significantQuestChange(base, renamed) {
		t.Fatal("title")
	}
	party := base
	party.PartyQuest = true
	if !significantQuestChange(base, party) {
		t.Fatal("party")
	}
}

func TestDecodeCover(t *testing.T) {
	png := tinyPNG(t)
	got, ct, err := decodeCover(encodePNG(png))
	if err != nil || ct != "image/png" || len(got) == 0 {
		t.Fatalf("cover %d %s %v", len(got), ct, err)
	}
	if _, _, err := decodeCover("not base64!!!"); err == nil {
		t.Fatal("expected invalid image_base64")
	}
	empty, _, err := decodeCover("  ")
	if err != nil || len(empty) != 0 {
		t.Fatal(err)
	}
}
