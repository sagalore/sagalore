package main

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ErrUnscopedSagas means quests have no group and SAGALORE_BOOTSTRAP_GROUP is unset.
var ErrUnscopedSagas = errors.New("sagas have no group_id and SAGALORE_BOOTSTRAP_GROUP is unset (expected slug:Name)")

// Bootstrap attaches unscoped sagas to the group named by spec ("slug:Name").
// A fresh database with no unscoped sagas does nothing, even when spec is set.
// When the group's webhook is still empty and hook is set, it is copied once.
func (s *Store) Bootstrap(ctx context.Context, spec, ownerTag, hook string) error {
	var unscoped int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM quests WHERE group_id IS NULL`).Scan(&unscoped); err != nil {
		return err
	}
	spec = strings.TrimSpace(spec)
	hook = strings.TrimSpace(hook)
	if unscoped == 0 {
		if spec == "" {
			return nil
		}
		slug, _, err := splitBootstrap(spec)
		if err != nil {
			return err
		}
		return s.copyBootstrapWebhook(ctx, slug, hook)
	}
	if spec == "" {
		return ErrUnscopedSagas
	}
	slug, name, err := splitBootstrap(spec)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	ownerID, err := bootstrapOwner(ctx, tx, ownerTag)
	if err != nil {
		return err
	}
	var groupID string
	err = tx.QueryRow(ctx, `SELECT id::text FROM groups WHERE slug=$1`, slug).Scan(&groupID)
	if errors.Is(err, pgx.ErrNoRows) {
		err = tx.QueryRow(ctx, `
			INSERT INTO groups (slug, name, locale, owner_player_id)
			VALUES ($1,$2,'en',$3) RETURNING id::text`, slug, name, ownerID).Scan(&groupID)
	}
	if err != nil {
		return err
	}
	if ownerID != nil {
		if _, err := tx.Exec(ctx, `UPDATE groups SET owner_player_id=$2 WHERE id=$1 AND owner_player_id IS NULL`, groupID, ownerID); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO group_members (group_id, player_id, role)
		SELECT $1, p.id, CASE WHEN $2::uuid IS NOT NULL AND p.id=$2 THEN 'owner' ELSE 'member' END
		FROM players p
		ON CONFLICT (group_id, player_id) DO UPDATE
		SET role = CASE WHEN EXCLUDED.role = 'owner' THEN 'owner' ELSE group_members.role END`, groupID, ownerID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
		UPDATE characters c SET participating = true
		FROM group_members gm
		WHERE gm.group_id=$1 AND gm.player_id = c.player_id`, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE quests SET group_id=$1 WHERE group_id IS NULL`, groupID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE adventure_log SET group_id=$1 WHERE group_id IS NULL`, groupID); err != nil {
		return err
	}
	if hook != "" {
		if _, err := tx.Exec(ctx, `
			UPDATE groups SET webhook_url=$2, webhook_sourced=true
			WHERE id=$1 AND webhook_url='' AND webhook_sourced=false`, groupID, hook); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) copyBootstrapWebhook(ctx context.Context, slug, hook string) error {
	if hook == "" || slug == "" {
		return nil
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE groups SET webhook_url=$2, webhook_sourced=true
		WHERE slug=$1 AND webhook_url='' AND webhook_sourced=false`, slug, hook)
	return err
}

func splitBootstrap(spec string) (slug, name string, err error) {
	slug, name, ok := strings.Cut(spec, ":")
	slug = strings.ToLower(strings.TrimSpace(slug))
	name = strings.TrimSpace(name)
	if !ok || slug == "" || name == "" {
		return "", "", fmt.Errorf("%w: SAGALORE_BOOTSTRAP_GROUP must be slug:Name", ErrInvalid)
	}
	if err := validateSlug(slug); err != nil {
		return "", "", err
	}
	return slug, name, nil
}

func bootstrapOwner(ctx context.Context, tx querier, ownerTag string) (any, error) {
	ownerTag = strings.TrimSpace(ownerTag)
	if ownerTag != "" {
		var id string
		err := tx.QueryRow(ctx, `SELECT id::text FROM players WHERE lower(battletag)=lower($1)`, ownerTag).Scan(&id)
		if err == nil {
			return id, nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	}
	var id string
	err := tx.QueryRow(ctx, `
		SELECT id::text FROM players
		WHERE battletag IS NOT NULL AND battletag <> ''
		ORDER BY created_at ASC LIMIT 1`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return id, nil
}
