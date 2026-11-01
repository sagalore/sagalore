package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/sagalore/sagalore/internal/contract"
)

// Map positions are fractions of UiMap 37 (Elwynn Forest) on retail, Classic Era,
// and Mists Classic. Lion's Pride Inn / Goldshire is about 43.8, 65.8. The north
// road where Falkhaan Isenstrider stands is about 45.6, 47.7. Innkeeper Farley,
// Refreshing Spring Water (159), and Ice Cold Milk (1179) exist in all three.
// NPC names are enUS because UnitName follows the client language.
func seedQuests() []QuestInput {
	goldX, goldY := 0.438, 0.658
	goldR := 0.05
	roadX, roadY := 0.456, 0.477
	roadR := 0.04
	water := 159
	milk := 1179
	one := 1
	return []QuestInput{
		{
			Title: "An evening in Goldshire",
			Description: "Go to Lion's Pride Inn in the heart of Goldshire.\n\n" +
				"Talk to Innkeeper Farley about the day's rumors, and make sure you have spring water in your bag before you sit by the fire.",
			PartyQuest: true,
			CreatedBy:  "Player#1234",
			Objectives: []contract.Objective{
				{ID: "goldshire", Type: contract.ObjectiveLocation, Text: "Stand in or just outside Lion's Pride Inn in Goldshire", Subzone: "Goldshire|Lion's Pride Inn", MapID: intPtr(37), X: &goldX, Y: &goldY, Radius: &goldR},
				{ID: "farley", Type: contract.ObjectiveInteract, Text: "Talk to Innkeeper Farley", NPC: "Innkeeper Farley"},
				{ID: "water", Type: contract.ObjectiveCollect, Text: "Have 1 Refreshing Spring Water (sold by Farley)", ItemID: &water, ItemName: "Refreshing Spring Water", Count: one},
			},
		},
		{
			Title: "The road from Northshire",
			Description: "North of Goldshire is the road out of Northshire Abbey.\n\n" +
				"Stand where Falkhaan Isenstrider usually stands, where the forest opens toward Elwynn.",
			PartyQuest: true,
			CreatedBy:  "Player#1234",
			Objectives: []contract.Objective{
				{ID: "road", Type: contract.ObjectiveLocation, Text: "Stand on the road south of Northshire", MapID: intPtr(37), X: &roadX, Y: &roadY, Radius: &roadR},
			},
		},
		{
			Title: "Milk for the watch",
			Description: "The guards in Goldshire need something cold to drink.\n\n" +
				"Ice Cold Milk is sold by Innkeeper Farley — buy one and meet on the square in front of the inn.",
			PartyQuest: true,
			CreatedBy:  "Player#1234",
			Objectives: []contract.Objective{
				{ID: "square", Type: contract.ObjectiveLocation, Text: "Stand in Goldshire", Subzone: "Goldshire|Lion's Pride Inn", MapID: intPtr(37), X: &goldX, Y: &goldY, Radius: &goldR},
				{ID: "milk", Type: contract.ObjectiveCollect, Text: "Have 1 Ice Cold Milk", ItemID: &milk, ItemName: "Ice Cold Milk", Count: one},
			},
		},
	}
}

func intPtr(n int) *int { return &n }

func seed(ctx context.Context, store *Store) (created, updated int, err error) {
	if _, err = store.EnsurePlayer(ctx, "Player#1234", "Player"); err != nil {
		return 0, 0, fmt.Errorf("system creator: %w", err)
	}
	for _, q := range seedQuests() {
		id, findErr := store.QuestIDByTitle(ctx, q.Title)
		if errors.Is(findErr, ErrNotFound) {
			if _, err = store.CreateQuest(ctx, q); err != nil {
				return created, updated, fmt.Errorf("%s: %w", q.Title, err)
			}
			created++
			continue
		}
		if findErr != nil {
			return created, updated, findErr
		}
		objectives := q.Objectives
		active := true
		if _, err = store.UpdateQuest(ctx, id, QuestPatch{
			Description: &q.Description,
			Objectives:  &objectives,
			PartyQuest:  &q.PartyQuest,
			Active:      &active,
			CreatedBy:   &q.CreatedBy,
			Changes:     []string{"Oppdatert fra seed"},
		}); err != nil {
			return created, updated, fmt.Errorf("%s: %w", q.Title, err)
		}
		updated++
	}
	return created, updated, nil
}
