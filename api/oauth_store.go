package main

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	oauthScopePlayer      = "player"
	oauthAccessTTL        = time.Hour
	oauthRefreshTTL       = 30 * 24 * time.Hour
	oauthCodeTTL          = 10 * time.Minute
	oauthRequestTTL       = 15 * time.Minute
	oauthApproveReplayTTL = 2 * time.Minute
	oauthHandoffTTL       = 2 * time.Minute
)

// errOAuthRequestExpired means the authorization request was locked and deleted
// because it had already expired. A missing row is ErrNotFound instead, so a
// repeat Allow can look for a replay of the code.
var errOAuthRequestExpired = errors.New("oauth authorization request expired")

type oauthFailure struct {
	Code string
	Desc string
}

func (e *oauthFailure) Error() string { return e.Code }

func oauthInvalidGrant(desc string) error {
	return &oauthFailure{Code: "invalid_grant", Desc: desc}
}

type oauthClientRecord struct {
	ClientID   string
	SecretHash string
	Name       string
	Redirects  []string
	Grants     []string
	Responses  []string
	AuthMethod string
	Scope      string
	Created    time.Time
}

type oauthClientDraft struct {
	Name       string
	Redirects  []string
	Grants     []string
	Responses  []string
	AuthMethod string
	Scope      string
	Secret     string
}

type oauthRequestRow struct {
	ID                 string
	ClientID           string
	RedirectURI        string
	State              string
	Challenge          string
	Scope              string
	Resource           string
	CSRFHash           string
	ConsentSessionHash string
}

type oauthApproveReplay struct {
	Code               string
	RedirectURI        string
	State              string
	CSRFHash           string
	ConsentSessionHash string
}

type oauthTokens struct {
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
	Scope        string
	Resource     string
}

type oauthCodeGrant struct {
	Code        string
	RedirectURI string
	Verifier    string
	Resource    string
	Issuer      string
}

type oauthRefreshGrant struct {
	Refresh  string
	Resource string
	Issuer   string
}

func (s *Store) CreateOAuthClient(ctx context.Context, in oauthClientDraft) (oauthClientRecord, error) {
	redirects, err := jsonText(in.Redirects)
	if err != nil {
		return oauthClientRecord{}, err
	}
	grants, err := jsonText(in.Grants)
	if err != nil {
		return oauthClientRecord{}, err
	}
	responses, err := jsonText(in.Responses)
	if err != nil {
		return oauthClientRecord{}, err
	}
	clientID, err := newToken("gsc_")
	if err != nil {
		return oauthClientRecord{}, err
	}
	var secretHash *string
	if in.Secret != "" {
		sum := hashKey(in.Secret)
		secretHash = &sum
	}
	var created time.Time
	err = s.pool.QueryRow(ctx, `
		INSERT INTO oauth_clients (
			client_id, client_secret_hash, client_name, redirect_uris, grant_types, response_types,
			token_endpoint_auth_method, scope)
		VALUES ($1,$2,$3,$4::jsonb,$5::jsonb,$6::jsonb,$7,$8)
		RETURNING created_at`,
		clientID, secretHash, in.Name, redirects, grants, responses, in.AuthMethod, in.Scope).Scan(&created)
	if err != nil {
		return oauthClientRecord{}, err
	}
	return oauthClientRecord{
		ClientID: clientID, SecretHash: deref(secretHash), Name: in.Name, Redirects: in.Redirects,
		Grants: in.Grants, Responses: in.Responses, AuthMethod: in.AuthMethod, Scope: in.Scope, Created: created,
	}, nil
}

func (s *Store) OAuthClient(ctx context.Context, clientID string) (oauthClientRecord, error) {
	var c oauthClientRecord
	var secret *string
	var redirects, grants, responses string
	err := s.pool.QueryRow(ctx, `
		SELECT client_id, client_secret_hash, client_name, redirect_uris::text, grant_types::text,
			response_types::text, token_endpoint_auth_method, scope, created_at
		FROM oauth_clients WHERE client_id=$1`, clientID).Scan(
		&c.ClientID, &secret, &c.Name, &redirects, &grants, &responses, &c.AuthMethod, &c.Scope, &c.Created)
	if errors.Is(err, pgx.ErrNoRows) {
		return oauthClientRecord{}, ErrNotFound
	}
	if err != nil {
		return oauthClientRecord{}, err
	}
	c.SecretHash = deref(secret)
	c.Redirects, err = parseJSONStrings(redirects)
	if err != nil {
		return oauthClientRecord{}, err
	}
	c.Grants, err = parseJSONStrings(grants)
	if err != nil {
		return oauthClientRecord{}, err
	}
	c.Responses, err = parseJSONStrings(responses)
	if err != nil {
		return oauthClientRecord{}, err
	}
	return c, nil
}

func (s *Store) CreateOAuthRequest(ctx context.Context, clientID, redirectURI, state, challenge, scope, resource string) (string, error) {
	var id string
	err := s.pool.QueryRow(ctx, `
		INSERT INTO oauth_requests (client_id, redirect_uri, state, code_challenge, scope, resource, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6, now() + ($7 * interval '1 second'))
		RETURNING id::text`,
		clientID, redirectURI, state, challenge, scope, resource, int(oauthRequestTTL.Seconds())).Scan(&id)
	return id, err
}

func (s *Store) OAuthRequest(ctx context.Context, id string) (oauthRequestRow, bool, error) {
	var row oauthRequestRow
	var expired bool
	err := s.pool.QueryRow(ctx, `
		SELECT id::text, client_id, redirect_uri, state, code_challenge, scope, resource,
			COALESCE(csrf_hash, ''), COALESCE(consent_session_hash, ''), (expires_at <= now())
		FROM oauth_requests WHERE id=$1`, id).Scan(
		&row.ID, &row.ClientID, &row.RedirectURI, &row.State, &row.Challenge, &row.Scope, &row.Resource,
		&row.CSRFHash, &row.ConsentSessionHash, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return oauthRequestRow{}, false, ErrNotFound
	}
	return row, expired, err
}

func (s *Store) DeleteOAuthRequest(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `DELETE FROM oauth_requests WHERE id=$1`, id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) IssueOAuthCode(ctx context.Context, requestID, playerID string) (string, oauthRequestRow, error) {
	if err := validUUID(playerID); err != nil {
		return "", oauthRequestRow{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return "", oauthRequestRow{}, err
	}
	defer tx.Rollback(ctx)
	var row oauthRequestRow
	var expired bool
	err = tx.QueryRow(ctx, `
		SELECT id::text, client_id, redirect_uri, state, code_challenge, scope, resource,
			COALESCE(csrf_hash, ''), COALESCE(consent_session_hash, ''), (expires_at <= now())
		FROM oauth_requests WHERE id=$1 FOR UPDATE`, requestID).Scan(
		&row.ID, &row.ClientID, &row.RedirectURI, &row.State, &row.Challenge, &row.Scope, &row.Resource,
		&row.CSRFHash, &row.ConsentSessionHash, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", oauthRequestRow{}, ErrNotFound
	}
	if err != nil {
		return "", oauthRequestRow{}, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM oauth_requests WHERE id=$1`, requestID); err != nil {
		return "", oauthRequestRow{}, err
	}
	if expired {
		if err := tx.Commit(ctx); err != nil {
			return "", oauthRequestRow{}, err
		}
		return "", row, errOAuthRequestExpired
	}
	raw, err := newToken("gso_")
	if err != nil {
		return "", oauthRequestRow{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO oauth_codes (code_hash, client_id, player_id, redirect_uri, code_challenge, scope, resource, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7, now() + ($8 * interval '1 second'))`,
		hashKey(raw), row.ClientID, playerID, row.RedirectURI, row.Challenge, row.Scope, row.Resource, int(oauthCodeTTL.Seconds())); err != nil {
		return "", oauthRequestRow{}, err
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO oauth_approve_replays
			(request_id, code, redirect_uri, state, csrf_hash, consent_session_hash, expires_at)
		VALUES ($1,$2,$3,$4,$5,$6, now() + ($7 * interval '1 second'))`,
		requestID, raw, row.RedirectURI, row.State, row.CSRFHash, row.ConsentSessionHash, int(oauthApproveReplayTTL.Seconds())); err != nil {
		return "", oauthRequestRow{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", oauthRequestRow{}, err
	}
	return raw, row, nil
}

func (s *Store) OAuthApproveReplay(ctx context.Context, requestID string) (oauthApproveReplay, error) {
	if err := validUUID(requestID); err != nil {
		return oauthApproveReplay{}, ErrNotFound
	}
	var replay oauthApproveReplay
	var expired bool
	err := s.pool.QueryRow(ctx, `
		SELECT code, redirect_uri, state, csrf_hash, consent_session_hash, (expires_at <= now())
		FROM oauth_approve_replays WHERE request_id=$1`, requestID).Scan(
		&replay.Code, &replay.RedirectURI, &replay.State, &replay.CSRFHash, &replay.ConsentSessionHash, &expired)
	if errors.Is(err, pgx.ErrNoRows) {
		return oauthApproveReplay{}, ErrNotFound
	}
	if err != nil {
		return oauthApproveReplay{}, err
	}
	if expired {
		_, _ = s.pool.Exec(ctx, `DELETE FROM oauth_approve_replays WHERE request_id=$1`, requestID)
		return oauthApproveReplay{}, ErrNotFound
	}
	return replay, nil
}

func (s *Store) PutOAuthHandoff(ctx context.Context, ticket, target string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO oauth_handoffs (ticket_hash, target, expires_at)
		VALUES ($1,$2, now() + ($3 * interval '1 second'))`,
		hashKey(ticket), target, int(oauthHandoffTTL.Seconds()))
	return err
}

func (s *Store) OAuthHandoff(ctx context.Context, ticket string) (string, error) {
	if ticket == "" {
		return "", ErrNotFound
	}
	var target string
	err := s.pool.QueryRow(ctx, `
		SELECT target FROM oauth_handoffs
		WHERE ticket_hash=$1 AND expires_at > now()`, hashKey(ticket)).Scan(&target)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return target, err
}

func (s *Store) RedeemOAuthCode(ctx context.Context, client oauthClientRecord, in oauthCodeGrant) (oauthTokens, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return oauthTokens{}, err
	}
	defer tx.Rollback(ctx)
	var codeID, codeClient, playerID, redirectURI, challenge, scope, resource, family string
	var expired, used bool
	err = tx.QueryRow(ctx, `
		SELECT id::text, client_id, player_id::text, redirect_uri, code_challenge, scope, resource,
			COALESCE(family_id::text, ''), (expires_at <= now()), (used_at IS NOT NULL)
		FROM oauth_codes WHERE code_hash=$1 FOR UPDATE`, hashKey(in.Code)).Scan(
		&codeID, &codeClient, &playerID, &redirectURI, &challenge, &scope, &resource, &family, &expired, &used)
	if errors.Is(err, pgx.ErrNoRows) {
		return oauthTokens{}, oauthInvalidGrant("authorization code is invalid")
	}
	if err != nil {
		return oauthTokens{}, err
	}
	if used {
		if err := revokeOAuthFamily(ctx, tx, family); err != nil {
			return oauthTokens{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return oauthTokens{}, err
		}
		return oauthTokens{}, oauthInvalidGrant("authorization code was already used")
	}
	reject := func(e error) (oauthTokens, error) {
		if _, err := tx.Exec(ctx, `UPDATE oauth_codes SET used_at=now() WHERE id=$1`, codeID); err != nil {
			return oauthTokens{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return oauthTokens{}, err
		}
		return oauthTokens{}, e
	}
	switch {
	case expired:
		return reject(oauthInvalidGrant("authorization code expired"))
	case codeClient != client.ClientID || redirectURI != in.RedirectURI:
		return reject(oauthInvalidGrant("authorization code is invalid"))
	case !oauthResourceMatch(resource, in.Resource, in.Issuer):
		return reject(&oauthFailure{Code: "invalid_target", Desc: "resource does not match"})
	case !pkceS256(in.Verifier, challenge):
		return reject(oauthInvalidGrant("code verifier does not match"))
	}
	tokens, familyID, err := insertOAuthPair(ctx, tx, playerID, client.ClientID, client.Name, "", nil, scope, resource)
	if err != nil {
		return oauthTokens{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE oauth_codes SET used_at=now(), family_id=$2 WHERE id=$1`, codeID, familyID); err != nil {
		return oauthTokens{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return oauthTokens{}, err
	}
	return tokens, nil
}

func (s *Store) RotateOAuthRefresh(ctx context.Context, client oauthClientRecord, in oauthRefreshGrant) (oauthTokens, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return oauthTokens{}, err
	}
	defer tx.Rollback(ctx)
	var family, tokenClient, playerID, scope, resource string
	var familyExpires time.Time
	var expired, rotated, revoked, familyDead bool
	err = tx.QueryRow(ctx, `
		SELECT family_id::text, client_id, player_id::text, scope, resource, family_expires_at,
			(expires_at <= now()), (rotated_at IS NOT NULL), (revoked_at IS NOT NULL), (family_expires_at <= now())
		FROM oauth_refresh_tokens WHERE token_hash=$1 FOR UPDATE`, hashKey(in.Refresh)).Scan(
		&family, &tokenClient, &playerID, &scope, &resource, &familyExpires, &expired, &rotated, &revoked, &familyDead)
	if errors.Is(err, pgx.ErrNoRows) {
		return oauthTokens{}, oauthInvalidGrant("refresh token is invalid")
	}
	if err != nil {
		return oauthTokens{}, err
	}
	if tokenClient != client.ClientID {
		return oauthTokens{}, oauthInvalidGrant("refresh token is invalid")
	}
	if rotated || revoked {
		if err := revokeOAuthFamily(ctx, tx, family); err != nil {
			return oauthTokens{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return oauthTokens{}, err
		}
		return oauthTokens{}, oauthInvalidGrant("refresh token was already used")
	}
	if expired || familyDead {
		if _, err := tx.Exec(ctx, `
			UPDATE oauth_refresh_tokens SET revoked_at=now() WHERE token_hash=$1 AND revoked_at IS NULL`, hashKey(in.Refresh)); err != nil {
			return oauthTokens{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return oauthTokens{}, err
		}
		return oauthTokens{}, oauthInvalidGrant("refresh token expired")
	}
	if !oauthResourceMatch(resource, in.Resource, in.Issuer) {
		return oauthTokens{}, &oauthFailure{Code: "invalid_target", Desc: "resource does not match"}
	}
	if _, err := tx.Exec(ctx, `
		UPDATE oauth_refresh_tokens SET rotated_at=now() WHERE token_hash=$1`, hashKey(in.Refresh)); err != nil {
		return oauthTokens{}, err
	}
	tokens, _, err := insertOAuthPair(ctx, tx, playerID, client.ClientID, client.Name, family, &familyExpires, scope, resource)
	if err != nil {
		return oauthTokens{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return oauthTokens{}, err
	}
	return tokens, nil
}

// RevokeOAuthToken revokes a refresh token's whole family, or a single access
// token issued to this client. An unknown token is not an error (RFC 7009).
func (s *Store) RevokeOAuthToken(ctx context.Context, clientID, raw, hint string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	accessFirst := hint == "access_token"
	if accessFirst {
		if _, err := revokeOAuthAccess(ctx, tx, clientID, raw); err != nil {
			return err
		}
		if _, err := revokeOAuthRefresh(ctx, tx, clientID, raw); err != nil {
			return err
		}
	} else {
		if _, err := revokeOAuthRefresh(ctx, tx, clientID, raw); err != nil {
			return err
		}
		if _, err := revokeOAuthAccess(ctx, tx, clientID, raw); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func revokeOAuthRefresh(ctx context.Context, tx pgx.Tx, clientID, raw string) (bool, error) {
	var family string
	err := tx.QueryRow(ctx, `
		SELECT family_id::text FROM oauth_refresh_tokens
		WHERE token_hash=$1 AND client_id=$2 FOR UPDATE`, hashKey(raw), clientID).Scan(&family)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := revokeOAuthFamily(ctx, tx, family); err != nil {
		return false, err
	}
	return true, nil
}

func revokeOAuthAccess(ctx context.Context, tx pgx.Tx, clientID, raw string) (bool, error) {
	tag, err := tx.Exec(ctx, `
		UPDATE personal_tokens SET revoked_at=now()
		WHERE token_hash=$1 AND oauth_client_id=$2 AND revoked_at IS NULL`, hashKey(raw), clientID)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() > 0, nil
}

func revokeOAuthFamily(ctx context.Context, q querier, family string) error {
	if family == "" {
		return nil
	}
	if _, err := q.Exec(ctx, `
		UPDATE oauth_refresh_tokens SET revoked_at=now()
		WHERE family_id=$1 AND revoked_at IS NULL`, family); err != nil {
		return err
	}
	_, err := q.Exec(ctx, `
		UPDATE personal_tokens SET revoked_at=now()
		WHERE oauth_family_id=$1 AND revoked_at IS NULL`, family)
	return err
}

func insertOAuthPair(ctx context.Context, q querier, playerID, clientID, clientName, family string, familyExpires *time.Time, scope, resource string) (oauthTokens, string, error) {
	access, err := newToken("gst_")
	if err != nil {
		return oauthTokens{}, "", err
	}
	refresh, err := newToken("gsr_")
	if err != nil {
		return oauthTokens{}, "", err
	}
	var familyID string
	var familyExpiresArg any
	if familyExpires != nil {
		familyExpiresArg = *familyExpires
	}
	err = q.QueryRow(ctx, `
		INSERT INTO oauth_refresh_tokens (
			token_hash, family_id, client_id, player_id, scope, resource, family_expires_at, expires_at)
		VALUES (
			$1,
			COALESCE(NULLIF($2, '')::uuid, gen_random_uuid()),
			$3, $4, $5, $6,
			COALESCE($8::timestamptz, now() + ($9 * interval '1 second')),
			LEAST(
				now() + ($7 * interval '1 second'),
				COALESCE($8::timestamptz, now() + ($9 * interval '1 second'))
			)
		)
		RETURNING family_id::text`,
		hashKey(refresh), family, clientID, playerID, scope, resource,
		int(oauthRefreshTTL.Seconds()), familyExpiresArg, int(oauthFamilyCap.Seconds())).Scan(&familyID)
	if err != nil {
		return oauthTokens{}, "", err
	}
	if _, err := q.Exec(ctx, `
		INSERT INTO personal_tokens (player_id, token_hash, label, expires_at, oauth_client_id, oauth_family_id)
		VALUES ($1,$2,$3, now() + ($4 * interval '1 second'), $5, $6)`,
		playerID, hashKey(access), oauthTokenLabel(clientName), int(oauthAccessTTL.Seconds()), clientID, familyID); err != nil {
		return oauthTokens{}, "", err
	}
	return oauthTokens{
		AccessToken: access, RefreshToken: refresh, ExpiresIn: int(oauthAccessTTL.Seconds()),
		Scope: scope, Resource: resource,
	}, familyID, nil
}

func (s *Store) BindOAuthConsent(ctx context.Context, requestID, consent, csrf string) error {
	tag, err := s.pool.Exec(ctx, `
		UPDATE oauth_requests
		SET csrf_hash=$2, consent_session_hash=$3
		WHERE id=$1 AND expires_at > now()`, requestID, hashKey(csrf), hashKey(consent))
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) AllowOAuthRegistration(ctx context.Context, ip string) (bool, error) {
	var n int
	if err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM oauth_register_attempts
		WHERE ip=$1 AND created_at > now() - interval '1 hour'`, ip).Scan(&n); err != nil {
		return false, err
	}
	if n >= oauthRegisterPerHour {
		return false, nil
	}
	_, err := s.pool.Exec(ctx, `INSERT INTO oauth_register_attempts (ip) VALUES ($1)`, ip)
	return err == nil, err
}

func (s *Store) CleanupOAuth(ctx context.Context) error {
	statements := []string{
		`DELETE FROM oauth_requests WHERE expires_at < now()`,
		`DELETE FROM oauth_approve_replays WHERE expires_at < now()`,
		`DELETE FROM oauth_handoffs WHERE expires_at < now()`,
		`DELETE FROM oauth_codes WHERE expires_at < now() AND used_at IS NULL`,
		`DELETE FROM oauth_codes WHERE used_at IS NOT NULL AND used_at < now() - interval '1 day'`,
		`DELETE FROM oauth_refresh_tokens
		 WHERE family_expires_at < now() - interval '1 day'
		    OR (expires_at < now() AND rotated_at IS NULL AND revoked_at IS NULL)`,
		`DELETE FROM oauth_consent_sessions WHERE expires_at < now()`,
		`DELETE FROM bnet_attempts WHERE expires_at < now()`,
		`DELETE FROM oauth_register_attempts WHERE created_at < now() - interval '1 day'`,
		`DELETE FROM oauth_clients c
		 WHERE c.created_at < now() - interval '7 days'
		   AND NOT EXISTS (SELECT 1 FROM oauth_requests r WHERE r.client_id = c.client_id)
		   AND NOT EXISTS (SELECT 1 FROM oauth_codes d WHERE d.client_id = c.client_id)
		   AND NOT EXISTS (SELECT 1 FROM oauth_refresh_tokens t WHERE t.client_id = c.client_id)
		   AND NOT EXISTS (SELECT 1 FROM personal_tokens p WHERE p.oauth_client_id = c.client_id)`,
	}
	for _, stmt := range statements {
		if _, err := s.pool.Exec(ctx, stmt); err != nil {
			return err
		}
	}
	return nil
}

func oauthTokenLabel(name string) string {
	label := "oauth:" + strings.TrimSpace(name)
	if label == "oauth:" {
		label = "oauth"
	}
	runes := []rune(label)
	if len(runes) > 80 {
		label = string(runes[:80])
	}
	return label
}

func jsonText(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func parseJSONStrings(raw string) ([]string, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, err
	}
	return out, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
