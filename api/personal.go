package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/sagalore/sagalore/internal/contract"
)

// personalCreator is the BattleTag stamped on auto-cloned starter sagas.
const personalCreator = "Sagalore#0000"

// EnsurePersonalGroup creates one account-scoped Personal group with starter
// sagas if the player does not already own one. Safe to call on every Sync.
func (s *Store) EnsurePersonalGroup(ctx context.Context, playerID string) error {
	if err := validUUID(playerID); err != nil {
		return err
	}
	var existing string
	err := s.pool.QueryRow(ctx, `
		SELECT id::text FROM groups
		WHERE personal AND owner_player_id=$1`, playerID).Scan(&existing)
	if err == nil {
		if err := s.autoIncludePersonalCharacters(ctx, playerID, existing); err != nil {
			return err
		}
		if err := s.upgradePersonalStartersV2(ctx, existing); err != nil {
			return err
		}
		return s.ensurePersonalStarterCovers(ctx, existing)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}

	slug := personalSlug(playerID)
	if err := validateSlug(slug); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var groupID string
	err = tx.QueryRow(ctx, `
		INSERT INTO groups (slug, name, locale, owner_player_id, personal)
		VALUES ($1,'Personal','en',$2,true)
		RETURNING id::text`, slug, playerID).Scan(&groupID)
	if err != nil {
		// Concurrent Sync may have won the unique index race.
		if mapped := mapPG(err); errors.Is(mapped, ErrConflict) {
			return nil
		}
		return mapPG(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO group_members (group_id, player_id, role) VALUES ($1,$2,'owner')`, groupID, playerID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE characters SET participating = true WHERE player_id=$1`, playerID); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}

	if _, err := s.EnsurePlayer(ctx, personalCreator, "Sagalore"); err != nil {
		return fmt.Errorf("personal creator: %w", err)
	}
	for _, q := range personalStarterQuests() {
		if err := s.createPersonalStarter(ctx, groupID, q); err != nil {
			return err
		}
	}
	if err := s.autoIncludePersonalCharacters(ctx, playerID, groupID); err != nil {
		return err
	}
	return nil
}

func (s *Store) createPersonalStarter(ctx context.Context, groupID string, q QuestInput) error {
	q.GroupID = groupID
	q.CreatedBy = personalCreator
	created, err := s.CreateQuest(ctx, q)
	if err != nil {
		return fmt.Errorf("personal starter %q: %w", q.Title, err)
	}
	if cover := personalCoverPNG(q.Title); len(cover) > 0 {
		if _, err := s.writeQuestImage(ctx, created.ID, "image/png", cover); err != nil {
			return fmt.Errorf("personal starter cover %q: %w", q.Title, err)
		}
	}
	return nil
}

// ensurePersonalStarterCovers attaches sample covers to active personal
// starters that still lack an image (idempotent).
func (s *Store) ensurePersonalStarterCovers(ctx context.Context, groupID string) error {
	rows, err := s.pool.Query(ctx, `
		SELECT id::text, title FROM quests
		WHERE group_id=$1::uuid AND active AND created_by=$2
		  AND (image_bytes IS NULL OR octet_length(image_bytes)=0)`,
		groupID, personalCreator)
	if err != nil {
		return err
	}
	defer rows.Close()
	type row struct{ id, title string }
	var missing []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.title); err != nil {
			return err
		}
		missing = append(missing, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, r := range missing {
		cover := personalCoverPNG(r.title)
		if len(cover) == 0 {
			continue
		}
		if _, err := s.writeQuestImage(ctx, r.id, "image/png", cover); err != nil {
			return fmt.Errorf("personal starter cover %q: %w", r.title, err)
		}
	}
	return nil
}

// autoIncludePersonalCharacters marks every character on the account as
// participating and clears leftover Personal include-invites. Personal is
// account-scoped; there is no opt-in dialog.
func (s *Store) autoIncludePersonalCharacters(ctx context.Context, playerID, groupID string) error {
	if _, err := s.pool.Exec(ctx, `
		UPDATE characters SET participating=true, updated_at=now()
		WHERE player_id=$1 AND participating=false`, playerID); err != nil {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE group_invites SET status='accepted'
		WHERE group_id=$1::uuid AND status='pending'`, groupID); err != nil {
		return err
	}
	return nil
}

func personalSlug(playerID string) string {
	hex := strings.ReplaceAll(playerID, "-", "")
	if len(hex) > 8 {
		hex = hex[:8]
	}
	return "p-" + strings.ToLower(hex)
}

func (s *Store) groupIsPersonal(ctx context.Context, groupID string) (bool, error) {
	var personal bool
	err := s.pool.QueryRow(ctx, `SELECT personal FROM groups WHERE id=$1`, groupID).Scan(&personal)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, ErrNotFound
	}
	return personal, err
}

// upgradePersonalStartersV2 replaces outdated auto-seeded starters with the
// current sample set. Covers the first-wave trio and the sit-based rhythm
// (Classic /sit has no chat text). Idempotent once the current set is live.
func (s *Store) upgradePersonalStartersV2(ctx context.Context, groupID string) error {
	var oldCount int
	err := s.pool.QueryRow(ctx, `
		SELECT COUNT(*) FROM quests
		WHERE group_id=$1::uuid AND active
		  AND created_by=$2
		  AND (
		    title IN ('Say hello', 'Take a seat', 'Celebrate')
		    OR (title = 'A quick rhythm' AND objectives::text ILIKE '%"SIT"%')
		  )`,
		groupID, personalCreator).Scan(&oldCount)
	if err != nil || oldCount == 0 {
		return err
	}
	if _, err := s.pool.Exec(ctx, `
		UPDATE quests SET active=false, updated_at=now()
		WHERE group_id=$1::uuid AND active AND created_by=$2`,
		groupID, personalCreator); err != nil {
		return err
	}
	if _, err := s.EnsurePlayer(ctx, personalCreator, "Sagalore"); err != nil {
		return fmt.Errorf("personal creator: %w", err)
	}
	for _, q := range personalStarterQuests() {
		if err := s.createPersonalStarter(ctx, groupID, q); err != nil {
			return err
		}
	}
	return nil
}

// personalStarterQuests are zone- and faction-agnostic. Emotes work anywhere
// in the open world, so every new account can finish them on any character.
// They intentionally sample different saga shapes: a single action, a timed
// sequence, and a flourish — enough to show what authoring can do.
func personalStarterQuests() []QuestInput {
	return []QuestInput{
		{
			Title:       "Wave hello",
			Description: "Sagalore watches what you do in the world.\n\nType /wave anywhere to finish this first saga.",
			PartyQuest:  true,
			Objectives: []contract.Objective{
				{ID: "wave", Type: contract.ObjectiveEmote, Text: "Wave (/wave)", Emote: "WAVE"},
			},
		},
		{
			Title:       "A quick rhythm",
			Description: "Sagas can chain actions on a timer.\n\nKneel, then wave - both within 20 seconds - to clear this one.",
			PartyQuest:  true,
			Objectives: []contract.Objective{
				{
					ID:        "rhythm",
					Type:      contract.ObjectiveSequence,
					Text:      "Kneel, then wave (20s)",
					WithinSec: 20,
					Beats: []contract.Objective{
						// /sit has no chat text on Classic, so the first sample
						// beat uses /kneel (which does) and stays easy to verify.
						{ID: "kneel", Type: contract.ObjectiveEmote, Text: "Kneel (/kneel)", Emote: "KNEEL"},
						{ID: "wave2", Type: contract.ObjectiveEmote, Text: "Wave (/wave)", Emote: "WAVE"},
					},
				},
			},
		},
		{
			Title:       "Strike a pose",
			Description: "One last flourish before you invite friends or build your own.\n\nDance with /dance.",
			PartyQuest:  true,
			Objectives: []contract.Objective{
				{ID: "dance", Type: contract.ObjectiveEmote, Text: "Dance (/dance)", Emote: "DANCE"},
			},
		},
	}
}
