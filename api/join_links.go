package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sagalore/sagalore/internal/contract"
)

const (
	joinTokenPref      = "gsj_"
	defaultJoinLinkTTL = 7 * 24 * time.Hour
	maxJoinLinkTTL     = 90 * 24 * time.Hour
	minJoinLinkTTL     = time.Hour
)

// ensureMemberAndParticipate adds the account as a group member and marks only
// the given characters participating. Characters must already belong to playerID.
func ensureMemberAndParticipate(ctx context.Context, q querier, playerID, groupID string, characterIDs []string) error {
	if _, err := q.Exec(ctx, `
		INSERT INTO group_members (group_id, player_id, role) VALUES ($1,$2,'member')
		ON CONFLICT (group_id, player_id) DO NOTHING`, groupID, playerID); err != nil {
		return err
	}
	seen := map[string]bool{}
	for _, id := range characterIDs {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		if err := validUUID(id); err != nil {
			return fmt.Errorf("%w: character_id", ErrInvalid)
		}
		tag, err := q.Exec(ctx, `
			UPDATE characters SET participating=true, updated_at=now()
			WHERE id=$1 AND player_id=$2`, id, playerID)
		if err != nil {
			return err
		}
		if tag.RowsAffected() == 0 {
			return fmt.Errorf("%w: character", ErrForbidden)
		}
	}
	return nil
}

func (s *Store) ListMyCharacters(ctx context.Context, playerID string) ([]contract.Character, error) {
	if err := validUUID(playerID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, characterSelect+` WHERE c.player_id=$1 ORDER BY c.name, c.realm`, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanCharacters(rows)
}

func (s *Server) joinLinkURL(token string) string {
	base := strings.TrimRight(strings.TrimSpace(s.publicBaseURL), "/")
	path := "/join/" + token
	if base == "" {
		return path
	}
	return base + path
}

func (s *Store) CreateJoinLink(ctx context.Context, playerID, groupID, mode string, expiresInSec int, maxUses *int) (contract.JoinLink, string, error) {
	if err := validUUID(groupID); err != nil {
		return contract.JoinLink{}, "", err
	}
	personal, err := s.groupIsPersonal(ctx, groupID)
	if err != nil {
		return contract.JoinLink{}, "", err
	}
	if personal {
		return contract.JoinLink{}, "", fmt.Errorf("%w: personal group", ErrForbidden)
	}
	if err := s.requireOwner(ctx, playerID, groupID); err != nil {
		return contract.JoinLink{}, "", err
	}
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		mode = "gated"
	}
	if mode != "open" && mode != "gated" {
		return contract.JoinLink{}, "", fmt.Errorf("%w: mode", ErrInvalid)
	}
	ttl := defaultJoinLinkTTL
	if expiresInSec > 0 {
		ttl = time.Duration(expiresInSec) * time.Second
	}
	if ttl < minJoinLinkTTL || ttl > maxJoinLinkTTL {
		return contract.JoinLink{}, "", fmt.Errorf("%w: expires_in_sec", ErrInvalid)
	}
	if maxUses != nil && *maxUses < 1 {
		return contract.JoinLink{}, "", fmt.Errorf("%w: max_uses", ErrInvalid)
	}
	raw, err := newToken(joinTokenPref)
	if err != nil {
		return contract.JoinLink{}, "", err
	}
	var id, groupName string
	var created, expires time.Time
	var useCount int
	var max sql.NullInt32
	err = s.pool.QueryRow(ctx, `
		INSERT INTO group_join_links (group_id, token_hash, created_by, mode, expires_at, max_uses)
		VALUES ($1,$2,$3,$4, now() + ($5 * interval '1 second'), $6)
		RETURNING id::text, (SELECT name FROM groups WHERE id=$1), mode, expires_at, max_uses, use_count, created_at`,
		groupID, hashKey(raw), playerID, mode, int(ttl.Seconds()), maxUses).Scan(
		&id, &groupName, &mode, &expires, &max, &useCount, &created)
	if err != nil {
		return contract.JoinLink{}, "", err
	}
	out := contract.JoinLink{
		ID: id, GroupID: groupID, GroupName: groupName, Mode: mode,
		ExpiresAt: contract.FormatTime(expires), UseCount: useCount,
		CreatedAt: contract.FormatTime(created), Token: raw,
	}
	if max.Valid {
		v := int(max.Int32)
		out.MaxUses = &v
	}
	return out, raw, nil
}

func (s *Store) ListJoinLinks(ctx context.Context, playerID, groupID string) ([]contract.JoinLink, error) {
	if err := validUUID(groupID); err != nil {
		return nil, err
	}
	if err := s.requireOwner(ctx, playerID, groupID); err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `
		SELECT l.id::text, l.group_id::text, g.name, l.mode, l.expires_at, l.max_uses, l.use_count, l.revoked_at, l.created_at
		FROM group_join_links l
		JOIN groups g ON g.id = l.group_id
		WHERE l.group_id=$1
		ORDER BY l.created_at DESC`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.JoinLink
	for rows.Next() {
		var link contract.JoinLink
		var expires, created time.Time
		var max sql.NullInt32
		var revoked sql.NullTime
		if err := rows.Scan(&link.ID, &link.GroupID, &link.GroupName, &link.Mode, &expires, &max, &link.UseCount, &revoked, &created); err != nil {
			return nil, err
		}
		link.ExpiresAt = contract.FormatTime(expires)
		link.CreatedAt = contract.FormatTime(created)
		if max.Valid {
			v := int(max.Int32)
			link.MaxUses = &v
		}
		if revoked.Valid {
			link.RevokedAt = contract.FormatTime(revoked.Time)
		}
		out = append(out, link)
	}
	return out, rows.Err()
}

func (s *Store) RevokeJoinLink(ctx context.Context, playerID, groupID, linkID string) error {
	if err := validUUID(linkID); err != nil {
		return err
	}
	if err := s.requireOwner(ctx, playerID, groupID); err != nil {
		return err
	}
	tag, err := s.pool.Exec(ctx, `
		UPDATE group_join_links SET revoked_at=now()
		WHERE id=$1 AND group_id=$2 AND revoked_at IS NULL`, linkID, groupID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		var one int
		err := s.pool.QueryRow(ctx, `
			SELECT 1 FROM group_join_links WHERE id=$1 AND group_id=$2`, linkID, groupID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return fmt.Errorf("%w: already revoked", ErrConflict)
	}
	return nil
}

type joinLinkRow struct {
	ID        string
	GroupID   string
	GroupName string
	Mode      string
	ExpiresAt time.Time
	MaxUses   sql.NullInt32
	UseCount  int
	Revoked   bool
}

func (s *Store) joinLinkByToken(ctx context.Context, token string) (joinLinkRow, error) {
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, joinTokenPref) || len(token) < len(joinTokenPref)+16 {
		return joinLinkRow{}, ErrNotFound
	}
	var row joinLinkRow
	var revoked sql.NullTime
	err := s.pool.QueryRow(ctx, `
		SELECT l.id::text, l.group_id::text, g.name, l.mode, l.expires_at, l.max_uses, l.use_count, l.revoked_at
		FROM group_join_links l
		JOIN groups g ON g.id = l.group_id
		WHERE l.token_hash=$1`, hashKey(token)).Scan(
		&row.ID, &row.GroupID, &row.GroupName, &row.Mode, &row.ExpiresAt, &row.MaxUses, &row.UseCount, &revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return joinLinkRow{}, ErrNotFound
	}
	if err != nil {
		return joinLinkRow{}, err
	}
	row.Revoked = revoked.Valid
	return row, nil
}

func (row joinLinkRow) usable() error {
	if row.Revoked {
		return fmt.Errorf("%w: link revoked", ErrForbidden)
	}
	if time.Now().After(row.ExpiresAt) {
		return fmt.Errorf("%w: link expired", ErrForbidden)
	}
	if row.MaxUses.Valid && row.UseCount >= int(row.MaxUses.Int32) {
		return fmt.Errorf("%w: link exhausted", ErrForbidden)
	}
	return nil
}

func (s *Store) JoinLinkInfo(ctx context.Context, token, playerID string) (contract.JoinLinkInfo, error) {
	row, err := s.joinLinkByToken(ctx, token)
	if err != nil {
		return contract.JoinLinkInfo{}, err
	}
	if err := row.usable(); err != nil {
		return contract.JoinLinkInfo{}, err
	}
	info := contract.JoinLinkInfo{
		GroupID: row.GroupID, GroupName: row.GroupName, Mode: row.Mode,
		ExpiresAt: contract.FormatTime(row.ExpiresAt),
	}
	if playerID == "" {
		return info, nil
	}
	if err := validUUID(playerID); err != nil {
		return contract.JoinLinkInfo{}, err
	}
	var member int
	_ = s.pool.QueryRow(ctx, `
		SELECT 1 FROM group_members WHERE group_id=$1 AND player_id=$2`, row.GroupID, playerID).Scan(&member)
	info.AlreadyMember = member == 1
	chars, err := s.ListMyCharacters(ctx, playerID)
	if err != nil {
		return contract.JoinLinkInfo{}, err
	}
	if chars == nil {
		chars = []contract.Character{}
	}
	info.Characters = chars
	return info, nil
}

func (s *Store) RedeemJoinLink(ctx context.Context, playerID, token string, characterIDs []string) (contract.JoinRequest, error) {
	if err := validUUID(playerID); err != nil {
		return contract.JoinRequest{}, err
	}
	if len(characterIDs) == 0 {
		return contract.JoinRequest{}, fmt.Errorf("%w: character_ids required", ErrInvalid)
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return contract.JoinRequest{}, err
	}
	defer tx.Rollback(ctx)

	var row joinLinkRow
	var revoked sql.NullTime
	err = tx.QueryRow(ctx, `
		SELECT l.id::text, l.group_id::text, g.name, l.mode, l.expires_at, l.max_uses, l.use_count, l.revoked_at
		FROM group_join_links l
		JOIN groups g ON g.id = l.group_id
		WHERE l.token_hash=$1
		FOR UPDATE OF l`, hashKey(token)).Scan(
		&row.ID, &row.GroupID, &row.GroupName, &row.Mode, &row.ExpiresAt, &row.MaxUses, &row.UseCount, &revoked)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.JoinRequest{}, ErrNotFound
	}
	if err != nil {
		return contract.JoinRequest{}, err
	}
	row.Revoked = revoked.Valid
	if err := row.usable(); err != nil {
		return contract.JoinRequest{}, err
	}
	if err := ensureOwnedCharacters(ctx, tx, playerID, characterIDs); err != nil {
		return contract.JoinRequest{}, err
	}

	if row.Mode == "open" {
		if err := ensureMemberAndParticipate(ctx, tx, playerID, row.GroupID, characterIDs); err != nil {
			return contract.JoinRequest{}, err
		}
		if _, err := tx.Exec(ctx, `UPDATE group_join_links SET use_count=use_count+1 WHERE id=$1`, row.ID); err != nil {
			return contract.JoinRequest{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return contract.JoinRequest{}, err
		}
		out := contract.JoinRequest{
			LinkID: row.ID, GroupID: row.GroupID, GroupName: row.GroupName,
			PlayerID: playerID, Status: "approved", Mode: "open", CharacterIDs: dedupeIDs(characterIDs),
			CreatedAt: contract.FormatTime(time.Now().UTC()),
		}
		_ = s.pool.QueryRow(ctx, `
			SELECT COALESCE(battletag, ''), display_name FROM players WHERE id=$1`, playerID).Scan(
			&out.BattleTag, &out.DisplayName)
		return out, nil
	}

	var existingID, existingStatus string
	err = tx.QueryRow(ctx, `
		SELECT id::text, status FROM group_join_requests
		WHERE link_id=$1 AND player_id=$2`, row.ID, playerID).Scan(&existingID, &existingStatus)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return contract.JoinRequest{}, err
	}
	if existingID != "" {
		if existingStatus != "pending" {
			return contract.JoinRequest{}, fmt.Errorf("%w: request is %s", ErrConflict, existingStatus)
		}
		if _, err := tx.Exec(ctx, `DELETE FROM group_join_request_characters WHERE request_id=$1`, existingID); err != nil {
			return contract.JoinRequest{}, err
		}
		if err := insertRequestCharacters(ctx, tx, existingID, characterIDs); err != nil {
			return contract.JoinRequest{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return contract.JoinRequest{}, err
		}
		out, err := s.GetJoinRequest(ctx, existingID)
		if err != nil {
			return contract.JoinRequest{}, err
		}
		out.Mode = "gated"
		return out, nil
	}

	var reqID string
	var created time.Time
	err = tx.QueryRow(ctx, `
		INSERT INTO group_join_requests (link_id, group_id, player_id, status)
		VALUES ($1,$2,$3,'pending')
		RETURNING id::text, created_at`, row.ID, row.GroupID, playerID).Scan(&reqID, &created)
	if err != nil {
		return contract.JoinRequest{}, mapPG(err)
	}
	if err := insertRequestCharacters(ctx, tx, reqID, characterIDs); err != nil {
		return contract.JoinRequest{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE group_join_links SET use_count=use_count+1 WHERE id=$1`, row.ID); err != nil {
		return contract.JoinRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return contract.JoinRequest{}, err
	}
	out, err := s.GetJoinRequest(ctx, reqID)
	if err != nil {
		return contract.JoinRequest{}, err
	}
	out.Mode = "gated"
	return out, nil
}

func ensureOwnedCharacters(ctx context.Context, q querier, playerID string, characterIDs []string) error {
	for _, id := range dedupeIDs(characterIDs) {
		if err := validUUID(id); err != nil {
			return fmt.Errorf("%w: character_id", ErrInvalid)
		}
		var one int
		err := q.QueryRow(ctx, `
			SELECT 1 FROM characters WHERE id=$1 AND player_id=$2`, id, playerID).Scan(&one)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: character", ErrForbidden)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func insertRequestCharacters(ctx context.Context, q querier, requestID string, characterIDs []string) error {
	for _, id := range dedupeIDs(characterIDs) {
		if _, err := q.Exec(ctx, `
			INSERT INTO group_join_request_characters (request_id, character_id)
			VALUES ($1,$2) ON CONFLICT DO NOTHING`, requestID, id); err != nil {
			return err
		}
	}
	return nil
}

func dedupeIDs(ids []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

func (s *Store) GetJoinRequest(ctx context.Context, requestID string) (contract.JoinRequest, error) {
	if err := validUUID(requestID); err != nil {
		return contract.JoinRequest{}, err
	}
	var out contract.JoinRequest
	var created time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT r.id::text, r.link_id::text, r.group_id::text, g.name, r.player_id::text,
			COALESCE(p.battletag, ''), COALESCE(p.display_name, ''), r.status, r.created_at
		FROM group_join_requests r
		JOIN groups g ON g.id = r.group_id
		JOIN players p ON p.id = r.player_id
		WHERE r.id=$1`, requestID).Scan(
		&out.ID, &out.LinkID, &out.GroupID, &out.GroupName, &out.PlayerID,
		&out.BattleTag, &out.DisplayName, &out.Status, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.JoinRequest{}, ErrNotFound
	}
	if err != nil {
		return contract.JoinRequest{}, err
	}
	out.CreatedAt = contract.FormatTime(created)
	chars, err := s.requestCharacterIDs(ctx, requestID)
	if err != nil {
		return contract.JoinRequest{}, err
	}
	out.CharacterIDs = chars
	return out, nil
}

func (s *Store) requestCharacterIDs(ctx context.Context, requestID string) ([]string, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT character_id::text FROM group_join_request_characters
		WHERE request_id=$1 ORDER BY character_id`, requestID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	if out == nil {
		out = []string{}
	}
	return out, rows.Err()
}

func (s *Store) ListJoinRequests(ctx context.Context, playerID, groupID, status string) ([]contract.JoinRequest, error) {
	if err := validUUID(groupID); err != nil {
		return nil, err
	}
	if err := s.requireOwner(ctx, playerID, groupID); err != nil {
		return nil, err
	}
	status = strings.TrimSpace(status)
	if status == "" {
		status = "pending"
	}
	if status != "pending" && status != "approved" && status != "rejected" {
		return nil, fmt.Errorf("%w: status", ErrInvalid)
	}
	rows, err := s.pool.Query(ctx, `
		SELECT r.id::text, r.link_id::text, r.group_id::text, g.name, r.player_id::text,
			COALESCE(p.battletag, ''), COALESCE(p.display_name, ''), r.status, r.created_at
		FROM group_join_requests r
		JOIN groups g ON g.id = r.group_id
		JOIN players p ON p.id = r.player_id
		WHERE r.group_id=$1 AND r.status=$2
		ORDER BY r.created_at`, groupID, status)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []contract.JoinRequest
	for rows.Next() {
		var req contract.JoinRequest
		var created time.Time
		if err := rows.Scan(&req.ID, &req.LinkID, &req.GroupID, &req.GroupName, &req.PlayerID,
			&req.BattleTag, &req.DisplayName, &req.Status, &created); err != nil {
			return nil, err
		}
		req.CreatedAt = contract.FormatTime(created)
		out = append(out, req)
	}
	for i := range out {
		chars, err := s.requestCharacterIDs(ctx, out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].CharacterIDs = chars
	}
	return out, rows.Err()
}

func (s *Store) AnswerJoinRequest(ctx context.Context, ownerID, groupID, requestID, status string) (contract.JoinRequest, error) {
	if err := validUUID(requestID); err != nil {
		return contract.JoinRequest{}, err
	}
	if status != "approved" && status != "rejected" {
		return contract.JoinRequest{}, fmt.Errorf("%w: status", ErrInvalid)
	}
	if err := s.requireOwner(ctx, ownerID, groupID); err != nil {
		return contract.JoinRequest{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return contract.JoinRequest{}, err
	}
	defer tx.Rollback(ctx)

	var req contract.JoinRequest
	var created time.Time
	err = tx.QueryRow(ctx, `
		SELECT r.id::text, r.link_id::text, r.group_id::text, g.name, r.player_id::text, r.status, r.created_at
		FROM group_join_requests r
		JOIN groups g ON g.id = r.group_id
		WHERE r.id=$1 AND r.group_id=$2
		FOR UPDATE OF r`, requestID, groupID).Scan(
		&req.ID, &req.LinkID, &req.GroupID, &req.GroupName, &req.PlayerID, &req.Status, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.JoinRequest{}, ErrNotFound
	}
	if err != nil {
		return contract.JoinRequest{}, err
	}
	if req.Status != "pending" {
		return contract.JoinRequest{}, fmt.Errorf("%w: request is %s", ErrConflict, req.Status)
	}
	charRows, err := tx.Query(ctx, `
		SELECT character_id::text FROM group_join_request_characters
		WHERE request_id=$1 ORDER BY character_id`, requestID)
	if err != nil {
		return contract.JoinRequest{}, err
	}
	var chars []string
	for charRows.Next() {
		var id string
		if err := charRows.Scan(&id); err != nil {
			charRows.Close()
			return contract.JoinRequest{}, err
		}
		chars = append(chars, id)
	}
	charRows.Close()
	if err := charRows.Err(); err != nil {
		return contract.JoinRequest{}, err
	}
	if status == "approved" {
		if err := ensureMemberAndParticipate(ctx, tx, req.PlayerID, req.GroupID, chars); err != nil {
			return contract.JoinRequest{}, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE group_join_requests SET status=$2 WHERE id=$1`, requestID, status); err != nil {
		return contract.JoinRequest{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return contract.JoinRequest{}, err
	}
	out, err := s.GetJoinRequest(ctx, requestID)
	if err != nil {
		return contract.JoinRequest{}, err
	}
	out.Mode = "gated"
	return out, nil
}

func (s *Server) createJoinLink(w http.ResponseWriter, r *http.Request) {
	if rejectActorOverride(w, r, false) {
		return
	}
	var body struct {
		Mode         string `json:"mode"`
		ExpiresInSec int    `json:"expires_in_sec"`
		MaxUses      *int   `json:"max_uses"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	out, raw, err := s.store.CreateJoinLink(r.Context(), playerFrom(r).ID, r.PathValue("id"), body.Mode, body.ExpiresInSec, body.MaxUses)
	if err != nil {
		s.fail(w, err)
		return
	}
	out.Token = raw
	out.URL = s.joinLinkURL(raw)
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) listJoinLinks(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListJoinLinks(r.Context(), playerFrom(r).ID, r.PathValue("id"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if out == nil {
		out = []contract.JoinLink{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"join_links": out})
}

func (s *Server) revokeJoinLink(w http.ResponseWriter, r *http.Request) {
	if err := s.store.RevokeJoinLink(r.Context(), playerFrom(r).ID, r.PathValue("id"), r.PathValue("linkId")); err != nil {
		s.fail(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listJoinRequests(w http.ResponseWriter, r *http.Request) {
	status := strings.TrimSpace(r.URL.Query().Get("status"))
	out, err := s.store.ListJoinRequests(r.Context(), playerFrom(r).ID, r.PathValue("id"), status)
	if err != nil {
		s.fail(w, err)
		return
	}
	if out == nil {
		out = []contract.JoinRequest{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"join_requests": out})
}

func (s *Server) approveJoinRequest(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.AnswerJoinRequest(r.Context(), playerFrom(r).ID, r.PathValue("id"), r.PathValue("requestId"), "approved")
	if err != nil {
		s.fail(w, err)
		return
	}
	s.announceJoin(r.Context(), out.Mode, out, out.BattleTag)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) rejectJoinRequest(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.AnswerJoinRequest(r.Context(), playerFrom(r).ID, r.PathValue("id"), r.PathValue("requestId"), "rejected")
	if err != nil {
		s.fail(w, err)
		return
	}
	s.announceJoin(r.Context(), out.Mode, out, out.BattleTag)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) getJoinByToken(w http.ResponseWriter, r *http.Request) {
	playerID := ""
	if p, ok := s.optionalPlayer(r); ok {
		playerID = p.ID
	}
	out, err := s.store.JoinLinkInfo(r.Context(), r.PathValue("token"), playerID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) redeemJoinByToken(w http.ResponseWriter, r *http.Request) {
	var body struct {
		CharacterIDs []string `json:"character_ids"`
	}
	if err := readJSON(r, &body, 1<<16); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid", "bad json")
		return
	}
	player := playerFrom(r)
	out, err := s.store.RedeemJoinLink(r.Context(), player.ID, r.PathValue("token"), body.CharacterIDs)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.announceJoin(r.Context(), out.Mode, out, player.BattleTag)
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) listMyCharacters(w http.ResponseWriter, r *http.Request) {
	out, err := s.store.ListMyCharacters(r.Context(), playerFrom(r).ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	if out == nil {
		out = []contract.Character{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"characters": out})
}

func (s *Server) optionalPlayer(r *http.Request) (contract.Player, bool) {
	raw := bearer(r)
	if raw == "" {
		return contract.Player{}, false
	}
	p, err := s.store.PlayerByKey(r.Context(), raw)
	if err != nil {
		return contract.Player{}, false
	}
	return p, true
}
