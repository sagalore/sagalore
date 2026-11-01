package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/i18n"
	"github.com/sagalore/sagalore/internal/webhook"
)

var slugPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,40}[a-z0-9])?$`)

func validateSlug(slug string) error {
	slug = strings.TrimSpace(slug)
	if !slugPattern.MatchString(slug) {
		return fmt.Errorf("%w: group slug", ErrInvalid)
	}
	return nil
}

func slugify(name string) string {
	var b strings.Builder
	prev := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
			prev = false
			continue
		}
		if !prev && b.Len() > 0 {
			b.WriteByte('-')
			prev = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		s = "group"
	}
	if len(s) > 42 {
		s = strings.Trim(s[:42], "-")
	}
	return s
}

func (s *Store) resolveWriteGroup(ctx context.Context, playerID, slug string) (string, error) {
	slug = strings.TrimSpace(slug)
	if slug != "" {
		if err := validateSlug(slug); err != nil {
			return "", err
		}
		var id, role string
		err := s.pool.QueryRow(ctx, `
			SELECT g.id::text, gm.role
			FROM groups g
			JOIN group_members gm ON gm.group_id = g.id AND gm.player_id=$2
			WHERE g.slug=$1`, slug, playerID).Scan(&id, &role)
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrForbidden
		}
		if err != nil {
			return "", err
		}
		return id, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT group_id::text FROM group_members WHERE player_id=$1`, playerID)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return "", err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(ids) == 1 {
		return ids[0], nil
	}
	if len(ids) == 0 {
		return "", ErrForbidden
	}
	return "", fmt.Errorf("%w: group is required when you belong to more than one group", ErrInvalid)
}

func (s *Store) memberRole(ctx context.Context, groupID, playerID string) (string, error) {
	var role string
	err := s.pool.QueryRow(ctx, `
		SELECT role FROM group_members WHERE group_id=$1 AND player_id=$2`, groupID, playerID).Scan(&role)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrForbidden
	}
	return role, err
}

func (s *Store) ListGroupMembers(ctx context.Context, playerID, groupID string) ([]contract.GroupMemberRow, error) {
	if err := validUUID(groupID); err != nil {
		return nil, err
	}
	if _, err := s.memberRole(ctx, groupID, playerID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT p.id::text, COALESCE(p.battletag, ''), COALESCE(p.display_name, ''), gm.role, gm.created_at
		FROM group_members gm
		JOIN players p ON p.id = gm.player_id
		WHERE gm.group_id=$1
		ORDER BY CASE gm.role WHEN 'owner' THEN 0 ELSE 1 END, gm.created_at`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.GroupMemberRow
	for rows.Next() {
		var m contract.GroupMemberRow
		var joined time.Time
		if err := rows.Scan(&m.PlayerID, &m.BattleTag, &m.DisplayName, &m.Role, &joined); err != nil {
			return nil, err
		}
		m.JoinedAt = joined.UTC().Format(time.RFC3339)
		out = append(out, m)
	}
	return out, rows.Err()
}

func (s *Store) GroupsForPlayer(ctx context.Context, playerID string) ([]contract.GroupSummary, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT g.id::text, g.slug, g.name, g.locale, gm.role, COALESCE(g.owner_player_id::text, ''), g.personal
		FROM groups g
		JOIN group_members gm ON gm.group_id = g.id
		WHERE gm.player_id=$1
		ORDER BY g.personal DESC, g.name`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.GroupSummary
	for rows.Next() {
		var g contract.GroupSummary
		if err := rows.Scan(&g.ID, &g.Slug, &g.Name, &g.Locale, &g.Role, &g.OwnerID, &g.Personal); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func (s *Store) CreateGroup(ctx context.Context, playerID, slug, name, locale string) (contract.GroupSummary, error) {
	name = strings.TrimSpace(name)
	if name == "" || len([]rune(name)) > 80 {
		return contract.GroupSummary{}, fmt.Errorf("%w: group name", ErrInvalid)
	}
	slug = strings.ToLower(strings.TrimSpace(slug))
	if slug == "" {
		slug = slugify(name)
	}
	if err := validateSlug(slug); err != nil {
		return contract.GroupSummary{}, err
	}
	locale = i18n.Normalize(locale)
	if locale != i18n.EN && locale != i18n.NB {
		locale = i18n.EN
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return contract.GroupSummary{}, err
	}
	defer tx.Rollback(ctx)
	var id string
	err = tx.QueryRow(ctx, `
		INSERT INTO groups (slug, name, locale, owner_player_id)
		VALUES ($1,$2,$3,$4) RETURNING id::text`, slug, name, locale, playerID).Scan(&id)
	if err != nil {
		return contract.GroupSummary{}, mapPG(err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO group_members (group_id, player_id, role) VALUES ($1,$2,'owner')`, id, playerID); err != nil {
		return contract.GroupSummary{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return contract.GroupSummary{}, err
	}
	return contract.GroupSummary{ID: id, Slug: slug, Name: name, Locale: locale, Role: "owner", OwnerID: playerID}, nil
}

func (s *Store) CreateInvite(ctx context.Context, playerID, groupID, name, realm, battletag string) (contract.Invite, error) {
	if err := validUUID(groupID); err != nil {
		return contract.Invite{}, err
	}
	personal, err := s.groupIsPersonal(ctx, groupID)
	if err != nil {
		return contract.Invite{}, err
	}
	if personal {
		return contract.Invite{}, fmt.Errorf("%w: personal group", ErrForbidden)
	}
	role, err := s.memberRole(ctx, groupID, playerID)
	if err != nil {
		return contract.Invite{}, err
	}
	if role != "owner" {
		return contract.Invite{}, ErrForbidden
	}
	if err := validateName(name); err != nil {
		return contract.Invite{}, err
	}
	if err := validateRealm(realm); err != nil {
		return contract.Invite{}, err
	}
	battletag = strings.TrimSpace(battletag)
	if battletag != "" {
		if err := validateBattleTag(battletag); err != nil {
			return contract.Invite{}, err
		}
	}
	var id, groupName string
	var created time.Time
	err = s.pool.QueryRow(ctx, `
		INSERT INTO group_invites (group_id, character_name, realm, battletag, status)
		VALUES ($1,$2,$3,NULLIF($4,''),'pending')
		RETURNING id::text, (SELECT name FROM groups WHERE id=$1), created_at`,
		groupID, strings.TrimSpace(name), strings.TrimSpace(realm), battletag).Scan(&id, &groupName, &created)
	if err != nil {
		return contract.Invite{}, err
	}
	return contract.Invite{
		ID: id, GroupID: groupID, GroupName: groupName, Name: strings.TrimSpace(name),
		Realm: strings.TrimSpace(realm), BattleTag: battletag, Status: "pending", Kind: "join",
		InvitedAt: contract.FormatTime(created),
	}, nil
}

// SyncInvites creates include-invites for alts that are not participating, then lists pending invites.
// Personal groups are skipped: every character on the account belongs there without asking.
func (s *Store) SyncInvites(ctx context.Context, playerID string) ([]contract.Invite, error) {
	if _, err := s.pool.Exec(ctx, `
		INSERT INTO group_invites (group_id, character_name, realm, status)
		SELECT gm.group_id, c.name, c.realm, 'pending'
		FROM characters c
		JOIN group_members gm ON gm.player_id = c.player_id
		JOIN groups g ON g.id = gm.group_id
		WHERE c.player_id=$1 AND c.participating = false
		AND NOT g.personal
		AND NOT EXISTS (
			SELECT 1 FROM group_invites i
			WHERE i.group_id = gm.group_id
			AND lower(i.character_name)=lower(c.name)
			AND lower(i.realm)=lower(c.realm)
		)`, playerID); err != nil {
		return nil, err
	}
	return s.ListMyInvites(ctx, playerID)
}

// ListMyInvites returns pending invites addressed to this account.
func (s *Store) ListMyInvites(ctx context.Context, playerID string) ([]contract.Invite, error) {
	if err := validUUID(playerID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.id::text, i.group_id::text, g.name, i.character_name, i.realm, COALESCE(i.battletag, ''), i.status,
			CASE WHEN gm.player_id IS NULL THEN 'join' ELSE 'include' END, i.created_at
		FROM group_invites i
		JOIN groups g ON g.id = i.group_id
		JOIN players p ON p.id=$1
		LEFT JOIN group_members gm ON gm.group_id = i.group_id AND gm.player_id = p.id
		WHERE i.status = 'pending'
		AND (
			EXISTS (
				SELECT 1 FROM characters c
				WHERE c.player_id = p.id
				AND lower(c.name)=lower(i.character_name)
				AND lower(c.realm)=lower(i.realm)
			)
			OR (i.battletag IS NOT NULL AND i.battletag <> '' AND lower(i.battletag)=lower(COALESCE(p.battletag, '')))
		)
		ORDER BY i.created_at`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.Invite
	for rows.Next() {
		var inv contract.Invite
		var invited time.Time
		if err := rows.Scan(&inv.ID, &inv.GroupID, &inv.GroupName, &inv.Name, &inv.Realm, &inv.BattleTag, &inv.Status, &inv.Kind, &invited); err != nil {
			return nil, err
		}
		inv.InvitedAt = contract.FormatTime(invited)
		out = append(out, inv)
	}
	return out, rows.Err()
}

// ListGroupInvites returns pending outgoing invites for a group the caller belongs to.
func (s *Store) ListGroupInvites(ctx context.Context, playerID, groupID, status string) ([]contract.Invite, error) {
	if err := validUUID(groupID); err != nil {
		return nil, err
	}
	if _, err := s.memberRole(ctx, groupID, playerID); err != nil {
		return nil, err
	}
	if status != "pending" {
		return nil, fmt.Errorf("%w: status", ErrInvalid)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT i.id::text, i.group_id::text, g.name, i.character_name, i.realm, COALESCE(i.battletag, ''), i.status,
			CASE WHEN EXISTS (
				SELECT 1 FROM characters c
				JOIN group_members gm ON gm.player_id = c.player_id AND gm.group_id = i.group_id
				WHERE lower(c.name)=lower(i.character_name) AND lower(c.realm)=lower(i.realm)
			) THEN 'include' ELSE 'join' END,
			i.created_at
		FROM group_invites i
		JOIN groups g ON g.id = i.group_id
		WHERE i.group_id=$1 AND i.status=$2
		ORDER BY i.created_at`, groupID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.Invite
	for rows.Next() {
		var inv contract.Invite
		var invited time.Time
		if err := rows.Scan(&inv.ID, &inv.GroupID, &inv.GroupName, &inv.Name, &inv.Realm, &inv.BattleTag, &inv.Status, &inv.Kind, &invited); err != nil {
			return nil, err
		}
		inv.InvitedAt = contract.FormatTime(invited)
		out = append(out, inv)
	}
	return out, rows.Err()
}

// RevokeInvite deletes a pending invite. Only the group owner may revoke.
func (s *Store) RevokeInvite(ctx context.Context, actorID, groupID, inviteID string) error {
	if err := validUUID(inviteID); err != nil {
		return err
	}
	if err := s.requireOwner(ctx, actorID, groupID); err != nil {
		return err
	}
	var status string
	err := s.pool.QueryRow(ctx, `
		SELECT status FROM group_invites WHERE id=$1 AND group_id=$2`, inviteID, groupID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if status != "pending" {
		return fmt.Errorf("%w: invite is %s", ErrConflict, status)
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM group_invites WHERE id=$1 AND group_id=$2 AND status='pending'`, inviteID, groupID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AcceptInvite(ctx context.Context, playerID, inviteID string) (contract.Invite, error) {
	return s.answerInvite(ctx, playerID, inviteID, "accepted")
}

func (s *Store) DeclineInvite(ctx context.Context, playerID, inviteID string) (contract.Invite, error) {
	return s.answerInvite(ctx, playerID, inviteID, "declined")
}

func (s *Store) answerInvite(ctx context.Context, playerID, inviteID, status string) (contract.Invite, error) {
	if err := validUUID(inviteID); err != nil {
		return contract.Invite{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return contract.Invite{}, err
	}
	defer tx.Rollback(ctx)
	var inv contract.Invite
	var tag sql.NullString
	err = tx.QueryRow(ctx, `
		SELECT i.id::text, i.group_id::text, g.name, i.character_name, i.realm, i.battletag, i.status
		FROM group_invites i JOIN groups g ON g.id = i.group_id
		WHERE i.id=$1`, inviteID).Scan(&inv.ID, &inv.GroupID, &inv.GroupName, &inv.Name, &inv.Realm, &tag, &inv.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.Invite{}, ErrNotFound
	}
	if err != nil {
		return contract.Invite{}, err
	}
	if tag.Valid {
		inv.BattleTag = tag.String
	}
	if inv.Status != "pending" {
		return contract.Invite{}, fmt.Errorf("%w: invite is %s", ErrConflict, inv.Status)
	}
	var playerTag string
	if err := tx.QueryRow(ctx, `SELECT COALESCE(battletag, '') FROM players WHERE id=$1`, playerID).Scan(&playerTag); err != nil {
		return contract.Invite{}, err
	}
	if inv.BattleTag != "" && !strings.EqualFold(inv.BattleTag, playerTag) {
		return contract.Invite{}, ErrForbidden
	}
	var owner sql.NullString
	var charID sql.NullString
	err = tx.QueryRow(ctx, `
		SELECT id::text, player_id::text FROM characters
		WHERE lower(name)=lower($1) AND lower(realm)=lower($2)`, inv.Name, inv.Realm).Scan(&charID, &owner)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return contract.Invite{}, err
	}
	if owner.Valid && owner.String != playerID {
		return contract.Invite{}, fmt.Errorf("%w: character", ErrConflict)
	}
	if status == "accepted" {
		if !charID.Valid {
			if err := tx.QueryRow(ctx, `
				INSERT INTO characters (player_id, name, realm, participating)
				VALUES ($1,$2,$3,true) RETURNING id::text`, playerID, inv.Name, inv.Realm).Scan(&charID); err != nil {
				return contract.Invite{}, mapPG(err)
			}
		} else if _, err := tx.Exec(ctx, `UPDATE characters SET participating=true, updated_at=now() WHERE id=$1`, charID.String); err != nil {
			return contract.Invite{}, err
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO group_members (group_id, player_id, role) VALUES ($1,$2,'member')
			ON CONFLICT (group_id, player_id) DO NOTHING`, inv.GroupID, playerID); err != nil {
			return contract.Invite{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE group_invites SET status=$2 WHERE id=$1`, inviteID, status); err != nil {
		return contract.Invite{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return contract.Invite{}, err
	}
	inv.Status = status
	var member int
	_ = s.pool.QueryRow(ctx, `SELECT 1 FROM group_members WHERE group_id=$1 AND player_id=$2`, inv.GroupID, playerID).Scan(&member)
	if member == 1 && status == "accepted" {
		// Newly joined accounts are "join"; an account that was already a member is "include".
		// After accept the account is a member either way. Report the action that happened.
	}
	inv.Kind = "join"
	if status == "accepted" {
		var role string
		_ = s.pool.QueryRow(ctx, `SELECT role FROM group_members WHERE group_id=$1 AND player_id=$2`, inv.GroupID, playerID).Scan(&role)
		if role == "owner" || role == "member" {
			inv.Kind = "include"
		}
	}
	return inv, nil
}

func (s *Store) LeaveGroup(ctx context.Context, playerID, groupID string) error {
	if err := validUUID(groupID); err != nil {
		return err
	}
	personal, err := s.groupIsPersonal(ctx, groupID)
	if err != nil {
		return err
	}
	if personal {
		return fmt.Errorf("%w: personal group", ErrForbidden)
	}
	role, err := s.memberRole(ctx, groupID, playerID)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM group_members WHERE group_id=$1 AND player_id=$2`, groupID, playerID); err != nil {
		return err
	}
	if role == "owner" {
		var next sql.NullString
		_ = tx.QueryRow(ctx, `
			SELECT player_id::text FROM group_members WHERE group_id=$1 ORDER BY created_at LIMIT 1`, groupID).Scan(&next)
		if next.Valid {
			if _, err := tx.Exec(ctx, `UPDATE group_members SET role='owner' WHERE group_id=$1 AND player_id=$2`, groupID, next.String); err != nil {
				return err
			}
			if _, err := tx.Exec(ctx, `UPDATE groups SET owner_player_id=$2 WHERE id=$1`, groupID, next.String); err != nil {
				return err
			}
		} else if _, err := tx.Exec(ctx, `UPDATE groups SET owner_player_id=NULL WHERE id=$1`, groupID); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) RemoveMember(ctx context.Context, actorID, groupID, memberID string) error {
	if err := validUUID(groupID); err != nil {
		return err
	}
	if err := validUUID(memberID); err != nil {
		return err
	}
	role, err := s.memberRole(ctx, groupID, actorID)
	if err != nil {
		return err
	}
	if role != "owner" {
		return ErrForbidden
	}
	if actorID == memberID {
		return fmt.Errorf("%w: owner leaves with POST /leave", ErrInvalid)
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM group_members WHERE group_id=$1 AND player_id=$2 AND role<>'owner'`, groupID, memberID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SetGroupWebhook(ctx context.Context, playerID, groupID, webhook string) error {
	if err := s.requireOwner(ctx, playerID, groupID); err != nil {
		return err
	}
	webhook = strings.TrimSpace(webhook)
	if webhook != "" && !strings.HasPrefix(webhook, "https://") && !strings.HasPrefix(webhook, "http://") {
		return fmt.Errorf("%w: webhook url must be http(s)", ErrInvalid)
	}
	_, err := s.pool.Exec(ctx, `
		UPDATE groups SET webhook_url=$2, webhook_sourced=true WHERE id=$1`, groupID, webhook)
	return err
}

func (s *Store) SetGroupLocale(ctx context.Context, playerID, groupID, locale string) error {
	if err := s.requireOwner(ctx, playerID, groupID); err != nil {
		return err
	}
	locale = i18n.Normalize(locale)
	if locale != i18n.EN && locale != i18n.NB {
		return fmt.Errorf("%w: locale", ErrInvalid)
	}
	_, err := s.pool.Exec(ctx, `UPDATE groups SET locale=$2 WHERE id=$1`, groupID, locale)
	return err
}

func (s *Store) requireOwner(ctx context.Context, playerID, groupID string) error {
	if err := validUUID(groupID); err != nil {
		return err
	}
	role, err := s.memberRole(ctx, groupID, playerID)
	if err != nil {
		return err
	}
	if role != "owner" {
		return ErrForbidden
	}
	return nil
}

func (s *Store) SetParticipating(ctx context.Context, playerID, characterID string, participating bool) (contract.Character, error) {
	if err := validUUID(characterID); err != nil {
		return contract.Character{}, err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE characters SET participating=$3, updated_at=now()
		WHERE id=$1 AND player_id=$2`, characterID, playerID, participating)
	if err != nil {
		return contract.Character{}, err
	}
	if tag.RowsAffected() == 0 {
		return contract.Character{}, ErrNotFound
	}
	return s.GetCharacter(ctx, characterID)
}

func (s *Store) GroupWebhookForQuest(ctx context.Context, questID string) (url string, group webhook.GroupRef, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(g.webhook_url, ''), COALESCE(g.id::text, ''), COALESCE(g.slug, ''), COALESCE(g.name, '')
		FROM quests q
		LEFT JOIN groups g ON g.id = q.group_id
		WHERE q.id=$1`, questID).Scan(&url, &group.ID, &group.Slug, &group.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", webhook.GroupRef{}, nil
	}
	return url, group, err
}

func (s *Store) GroupWebhook(ctx context.Context, groupID string) (url string, group webhook.GroupRef, err error) {
	err = s.pool.QueryRow(ctx, `
		SELECT COALESCE(webhook_url, ''), id::text, slug, name
		FROM groups WHERE id=$1`, groupID).Scan(&url, &group.ID, &group.Slug, &group.Name)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", webhook.GroupRef{}, ErrNotFound
	}
	return url, group, err
}

func (s *Store) MintPersonalToken(ctx context.Context, playerID, label string) (contract.PersonalToken, error) {
	if err := validUUID(playerID); err != nil {
		return contract.PersonalToken{}, err
	}
	label = strings.TrimSpace(label)
	if label == "" {
		label = "personal"
	}
	if len([]rune(label)) > 80 {
		return contract.PersonalToken{}, fmt.Errorf("%w: label", ErrInvalid)
	}
	raw, err := newToken("gst_")
	if err != nil {
		return contract.PersonalToken{}, err
	}
	var id string
	var created time.Time
	err = s.pool.QueryRow(ctx, `
		INSERT INTO personal_tokens (player_id, token_hash, label)
		VALUES ($1,$2,$3) RETURNING id::text, created_at`, playerID, hashKey(raw), label).Scan(&id, &created)
	if err != nil {
		return contract.PersonalToken{}, err
	}
	return contract.PersonalToken{ID: id, Label: label, Token: raw, CreatedAt: contract.FormatTime(created)}, nil
}

func (s *Store) RevokePersonalToken(ctx context.Context, playerID, tokenID string) error {
	if err := validUUID(tokenID); err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var family string
	err = tx.QueryRow(ctx, `
		UPDATE personal_tokens SET revoked_at=now()
		WHERE id=$1 AND player_id=$2 AND revoked_at IS NULL
		RETURNING COALESCE(oauth_family_id::text, '')`, tokenID, playerID).Scan(&family)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	// An OAuth access token and its refresh tokens are one family. Revoking
	// the access token from the account should stop the client refreshing it.
	if err := revokeOAuthFamily(ctx, tx, family); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// MergeBattleNet finds a player by Battle.net account id, else by BattleTag, else inserts one.
func (s *Store) MergeBattleNet(ctx context.Context, accountID, battletag, displayName string) (contract.Player, error) {
	accountID = strings.TrimSpace(accountID)
	battletag = strings.TrimSpace(battletag)
	displayName = strings.TrimSpace(displayName)
	if accountID == "" || len(accountID) > 64 {
		return contract.Player{}, fmt.Errorf("%w: bnet account", ErrInvalid)
	}
	if err := validateBattleTag(battletag); err != nil {
		return contract.Player{}, err
	}
	if displayName == "" {
		displayName = battletag
		if i := strings.Index(displayName, "#"); i > 0 {
			displayName = displayName[:i]
		}
	}
	if len([]rune(displayName)) > 80 {
		displayName = string([]rune(displayName)[:80])
	}
	var p contract.Player
	var created time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, COALESCE(battletag, ''), display_name, COALESCE(bnet_account_id, ''), created_at
		FROM players WHERE bnet_account_id=$1`, accountID).Scan(&p.ID, &p.BattleTag, &p.DisplayName, &p.BnetAccountID, &created)
	if err == nil {
		if _, err := s.pool.Exec(ctx, `
			UPDATE players SET battletag=$2,
				display_name=CASE WHEN display_name='' THEN $3 ELSE display_name END
			WHERE id=$1`, p.ID, battletag, displayName); err != nil {
			return contract.Player{}, mapPG(err)
		}
		p.BattleTag = battletag
		if p.DisplayName == "" {
			p.DisplayName = displayName
		}
		scanPlayer(&p, created)
		return p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return contract.Player{}, err
	}
	err = s.pool.QueryRow(ctx, `
		SELECT id::text, COALESCE(battletag, ''), display_name, COALESCE(bnet_account_id, ''), created_at
		FROM players WHERE lower(battletag)=lower($1)`, battletag).Scan(&p.ID, &p.BattleTag, &p.DisplayName, &p.BnetAccountID, &created)
	if err == nil {
		if p.BnetAccountID != "" && p.BnetAccountID != accountID {
			return contract.Player{}, fmt.Errorf("%w: battletag belongs to another Battle.net account", ErrConflict)
		}
		if _, err := s.pool.Exec(ctx, `UPDATE players SET bnet_account_id=$2 WHERE id=$1`, p.ID, accountID); err != nil {
			return contract.Player{}, mapPG(err)
		}
		p.BnetAccountID = accountID
		scanPlayer(&p, created)
		return p, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return contract.Player{}, err
	}
	err = s.pool.QueryRow(ctx, `
		INSERT INTO players (battletag, display_name, bnet_account_id)
		VALUES ($1,$2,$3)
		RETURNING id::text, battletag, display_name, bnet_account_id, created_at`,
		battletag, displayName, accountID).Scan(&p.ID, &p.BattleTag, &p.DisplayName, &p.BnetAccountID, &created)
	if err != nil {
		return contract.Player{}, mapPG(err)
	}
	scanPlayer(&p, created)
	return p, nil
}

func (s *Store) DeleteSaga(ctx context.Context, id string) error {
	if err := validUUID(id); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM quests WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	if err := validUUID(id); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `DELETE FROM groups WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Server) createGroup(w http.ResponseWriter, r *http.Request) {
	if rejectActorOverride(w, r, false) {
		return
	}
	var body struct {
		Slug   string `json:"slug"`
		Name   string `json:"name"`
		Locale string `json:"locale"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	out, err := s.store.CreateGroup(r.Context(), playerFrom(r).ID, body.Slug, body.Name, body.Locale)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) listGroups(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.GroupsForPlayer(r.Context(), playerFrom(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if out == nil {
		out = []contract.GroupSummary{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"groups": out})
}

func (s *Server) listGroupMembers(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListGroupMembers(r.Context(), playerFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if out == nil {
		out = []contract.GroupMemberRow{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"members": out})
}

func (s *Server) createInvite(w http.ResponseWriter, r *http.Request) {
	if rejectActorOverride(w, r, false) {
		return
	}
	var body struct {
		Name      string `json:"name"`
		Realm     string `json:"realm"`
		BattleTag string `json:"battletag"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	out, err := s.store.CreateInvite(r.Context(), playerFrom(r).ID, r.PathValue("id"), body.Name, body.Realm, body.BattleTag)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) listGroupInvites(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	if status == "" {
		status = "pending"
	}
	if status != "pending" {
		writeErr(w, http.StatusBadRequest, "invalid", "status must be pending")
		return
	}
	out, err := s.store.ListGroupInvites(r.Context(), playerFrom(r).ID, r.PathValue("id"), status)
	if err != nil {
		s.fail(w, err)
		return
	}
	if out == nil {
		out = []contract.Invite{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": out})
}

func (s *Server) revokeInvite(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RevokeInvite(r.Context(), playerFrom(r).ID, r.PathValue("id"), r.PathValue("inviteId")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listMyInvites(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListMyInvites(r.Context(), playerFrom(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if out == nil {
		out = []contract.Invite{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"invites": out})
}

func (s *Server) acceptInvite(w http.ResponseWriter, r *http.Request) {
	p := playerFrom(r)
	out, err := s.store.AcceptInvite(r.Context(), p.ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.announceInviteAccepted(r.Context(), out, p.BattleTag)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) declineInvite(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.DeclineInvite(r.Context(), playerFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) leaveGroup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.LeaveGroup(r.Context(), playerFrom(r).ID, r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeMember(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RemoveMember(r.Context(), playerFrom(r).ID, r.PathValue("id"), r.PathValue("playerID")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) putGroupWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL string `json:"url"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	if err := s.store.SetGroupWebhook(r.Context(), playerFrom(r).ID, r.PathValue("id"), body.URL); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) putGroupLocale(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Locale string `json:"locale"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	if err := s.store.SetGroupLocale(r.Context(), playerFrom(r).ID, r.PathValue("id"), body.Locale); err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"locale": i18n.Normalize(body.Locale)})
}

func (s *Server) patchCharacterParticipating(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Participating *bool `json:"participating"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	if body.Participating == nil {
		writeErr(w, http.StatusBadRequest, "invalid", "participating is required")
		return
	}
	out, err := s.store.SetParticipating(r.Context(), playerFrom(r).ID, r.PathValue("id"), *body.Participating)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func rejectOAuthCredential(w http.ResponseWriter, r *http.Request) bool {
	if strings.TrimSpace(playerFrom(r).OAuthClientID) == "" {
		return false
	}
	writeErr(w, http.StatusForbidden, "oauth_token", "an OAuth access token cannot manage tokens")
	return true
}

func (s *Server) mintToken(w http.ResponseWriter, r *http.Request) {
	if rejectOAuthCredential(w, r) {
		return
	}
	var body struct {
		Label string `json:"label"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &body, 1<<16); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", "bad json")
			return
		}
	}
	out, err := s.store.MintPersonalToken(r.Context(), playerFrom(r).ID, body.Label)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) revokeToken(w http.ResponseWriter, r *http.Request) {
	if rejectOAuthCredential(w, r) {
		return
	}
	if err := s.store.RevokePersonalToken(r.Context(), playerFrom(r).ID, r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteAdminSaga(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteSaga(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteAdminGroup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteGroup(r.Context(), r.PathValue("id")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func rejectActorOverride(w http.ResponseWriter, r *http.Request, createdBy bool) bool {
	if createdBy {
		writeErr(w, http.StatusBadRequest, "invalid", "created_by is not accepted")
		return true
	}
	for _, h := range []string{
		"X-User", "X-User-Id", "X-User-Email", "X-Player", "X-Player-Id",
		"X-BattleTag", "X-Battletag", "X-Created-By", "X-Editor-Email",
	} {
		if strings.TrimSpace(r.Header.Get(h)) != "" {
			writeErr(w, http.StatusBadRequest, "invalid", "user identity headers are not accepted")
			return true
		}
	}
	return false
}

func (s *Server) withMember(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.adminToken != "" && subtleConstantTimeString(bearer(r), s.adminToken) == 1 {
			writeErr(w, http.StatusForbidden, "admin_cannot_author", "ADMIN_TOKEN cannot author sagas or change groups")
			return
		}
		s.withPlayer(next)(w, r)
	}
}
