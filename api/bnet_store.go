package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/sagalore/sagalore/internal/contract"
)

const (
	bnetAttemptTTL   = 30 * time.Minute
	oauthConsentTTL  = 15 * time.Minute
	oauthFamilyCap   = 90 * 24 * time.Hour
	oauthConsentPref = "goc_"
)

type bnetAttemptRecord struct {
	Kind           string
	ID             string
	Verifier       string
	State          string
	OAuthRequestID string
	Session        *sessionWire
	ErrCode        string
	ErrMsg         string
}

func (s *Store) UpsertBnetAttempt(ctx context.Context, row bnetAttemptRecord) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO bnet_attempts (kind, id, verifier, state, oauth_request_id, expires_at)
		VALUES ($1,$2,$3,$4,$5, now() + ($6 * interval '1 second'))
		ON CONFLICT (kind, id) DO UPDATE SET
			verifier = EXCLUDED.verifier,
			state = EXCLUDED.state,
			oauth_request_id = EXCLUDED.oauth_request_id,
			session_json = NULL,
			error_code = '',
			error_message = '',
			expires_at = EXCLUDED.expires_at,
			created_at = now()`,
		row.Kind, row.ID, row.Verifier, row.State, row.OAuthRequestID, int(bnetAttemptTTL.Seconds()))
	return err
}

func (s *Store) BnetAttemptByState(ctx context.Context, state string) (bnetAttemptRecord, error) {
	return s.scanBnetAttempt(ctx, `
		SELECT kind, id, verifier, state, oauth_request_id, COALESCE(session_json::text, ''), error_code, error_message
		FROM bnet_attempts WHERE state=$1 AND expires_at > now()`, state)
}

func (s *Store) BnetAttempt(ctx context.Context, kind, id string) (bnetAttemptRecord, error) {
	return s.scanBnetAttempt(ctx, `
		SELECT kind, id, verifier, state, oauth_request_id, COALESCE(session_json::text, ''), error_code, error_message
		FROM bnet_attempts WHERE kind=$1 AND id=$2 AND expires_at > now()`, kind, id)
}

func (s *Store) scanBnetAttempt(ctx context.Context, query string, args ...any) (bnetAttemptRecord, error) {
	var row bnetAttemptRecord
	var session string
	err := s.pool.QueryRow(ctx, query, args...).Scan(
		&row.Kind, &row.ID, &row.Verifier, &row.State, &row.OAuthRequestID, &session, &row.ErrCode, &row.ErrMsg)
	if errors.Is(err, pgx.ErrNoRows) {
		return bnetAttemptRecord{}, ErrNotFound
	}
	if err != nil {
		return bnetAttemptRecord{}, err
	}
	if session != "" {
		var wire sessionWire
		if err := json.Unmarshal([]byte(session), &wire); err != nil {
			return bnetAttemptRecord{}, err
		}
		row.Session = &wire
	}
	return row, nil
}

func (s *Store) SetBnetAttemptError(ctx context.Context, kind, id, code, msg string) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE bnet_attempts SET error_code=$3, error_message=$4
		WHERE kind=$1 AND id=$2`, kind, id, code, msg)
	return err
}

func (s *Store) SetBnetAttemptSession(ctx context.Context, kind, id string, wire sessionWire) error {
	raw, err := json.Marshal(wire)
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `
		UPDATE bnet_attempts SET session_json=$3::jsonb, error_code='', error_message=''
		WHERE kind=$1 AND id=$2`, kind, id, string(raw))
	return err
}

func (s *Store) DeleteBnetAttempt(ctx context.Context, kind, id string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM bnet_attempts WHERE kind=$1 AND id=$2`, kind, id)
	return err
}

func (s *Store) IssueConsentSession(ctx context.Context, playerID string) (string, error) {
	if err := validUUID(playerID); err != nil {
		return "", err
	}
	raw, err := newToken(oauthConsentPref)
	if err != nil {
		return "", err
	}
	_, err = s.pool.Exec(ctx, `
		INSERT INTO oauth_consent_sessions (token_hash, player_id, expires_at)
		VALUES ($1,$2, now() + ($3 * interval '1 second'))`,
		hashKey(raw), playerID, int(oauthConsentTTL.Seconds()))
	if err != nil {
		return "", err
	}
	return raw, nil
}

func (s *Store) PlayerByConsentSession(ctx context.Context, raw string) (contract.Player, error) {
	var p contract.Player
	var created time.Time
	err := s.pool.QueryRow(ctx, `
		SELECT p.id::text, COALESCE(p.battletag, ''), p.display_name, COALESCE(p.bnet_account_id, ''), p.created_at
		FROM oauth_consent_sessions c
		JOIN players p ON p.id = c.player_id
		WHERE c.token_hash=$1 AND c.expires_at > now()`, hashKey(raw)).Scan(
		&p.ID, &p.BattleTag, &p.DisplayName, &p.BnetAccountID, &created)
	if errors.Is(err, pgx.ErrNoRows) {
		return contract.Player{}, ErrForbidden
	}
	if err != nil {
		return contract.Player{}, err
	}
	scanPlayer(&p, created)
	return p, nil
}
