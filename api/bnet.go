package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	bnetAuthorizeURL  = "https://oauth.battle.net/authorize"
	bnetTokenURL      = "https://oauth.battle.net/token"
	bnetUserInfoURL   = "https://oauth.battle.net/userinfo"
	bnetKindCompanion = "companion"
	bnetKindOAuth     = "oauth"
)

type sessionWire struct {
	TokenType    string `json:"token_type"`
	SessionToken string `json:"session_token"`
	ExpiresAt    string `json:"expires_at"`
	Player       any    `json:"player"`
}

// bnetFlow is the HTTP client for Battle.net. Attempts themselves live in
// Postgres so a restart or another replica can finish the login.
type bnetFlow struct {
	mu           sync.Mutex
	client       *http.Client
	now          func() time.Time
	authorizeURL string
	tokenURL     string
	userInfoURL  string
}

var errBnetNotConfigured = errors.New("bnet not configured")

func newBnetFlow() *bnetFlow {
	return &bnetFlow{client: &http.Client{Timeout: 20 * time.Second}, now: time.Now}
}

func (f *bnetFlow) authorizeEndpoint() string {
	if f != nil && f.authorizeURL != "" {
		return f.authorizeURL
	}
	return bnetAuthorizeURL
}

func (f *bnetFlow) tokenEndpoint() string {
	if f != nil && f.tokenURL != "" {
		return f.tokenURL
	}
	return bnetTokenURL
}

func (f *bnetFlow) userInfoEndpoint() string {
	if f != nil && f.userInfoURL != "" {
		return f.userInfoURL
	}
	return bnetUserInfoURL
}

func (s *Server) bnetFlow() *bnetFlow {
	if s.bnet == nil {
		s.bnet = newBnetFlow()
	}
	return s.bnet
}

func (s *Server) bnetConfigured() bool {
	return strings.TrimSpace(s.bnetClientID) != "" && strings.TrimSpace(s.publicBaseURL) != ""
}

func (s *Server) battleNetAuthorizeURL(state, challenge string) string {
	q := url.Values{}
	q.Set("client_id", s.bnetClientID)
	q.Set("redirect_uri", s.bnetRedirect())
	q.Set("response_type", "code")
	q.Set("scope", "openid wow.profile")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	return s.bnetFlow().authorizeEndpoint() + "?" + q.Encode()
}

func (s *Server) bnetPollURL(attempt string) string {
	path := "/v1/auth/bnet/poll?attempt=" + url.QueryEscape(attempt)
	base := strings.TrimRight(strings.TrimSpace(s.publicBaseURL), "/")
	if base == "" {
		return path
	}
	return base + path
}

// registerBnetAttempt stores a companion PKCE attempt and returns its id and
// the Battle.net authorize URL. An empty id is generated. Companion ids are a
// different kind from OAuth consent attempts, so the same string cannot collide.
func (s *Server) registerBnetAttempt(ctx context.Context, id string) (string, string, error) {
	if !s.bnetConfigured() {
		return "", "", errBnetNotConfigured
	}
	id = strings.TrimSpace(id)
	if id == "" {
		var err error
		id, err = randomToken(16)
		if err != nil {
			return "", "", err
		}
	}
	if len(id) > 80 {
		return "", "", fmt.Errorf("%w: attempt", ErrInvalid)
	}
	verifier, challenge, err := pkcePair()
	if err != nil {
		return "", "", err
	}
	state, err := randomToken(16)
	if err != nil {
		return "", "", err
	}
	if err := s.store.UpsertBnetAttempt(ctx, bnetAttemptRecord{
		Kind: bnetKindCompanion, ID: id, Verifier: verifier, State: state,
	}); err != nil {
		return "", "", err
	}
	return id, s.battleNetAuthorizeURL(state, challenge), nil
}

func (s *Server) bnetStart(w http.ResponseWriter, r *http.Request) {
	attempt := strings.TrimSpace(r.URL.Query().Get("attempt"))
	if attempt == "" || len(attempt) > 80 {
		writeErr(w, http.StatusBadRequest, "invalid", "attempt is required")
		return
	}
	_, authURL, err := s.registerBnetAttempt(r.Context(), attempt)
	if err != nil {
		if errors.Is(err, errBnetNotConfigured) {
			writeErr(w, http.StatusServiceUnavailable, "bnet_not_configured", "BNET_CLIENT_ID and PUBLIC_BASE_URL are required")
			return
		}
		s.fail(w, err)
		return
	}
	http.Redirect(w, r, authURL, http.StatusFound)
}

// beginBnetLogin stores an OAuth consent attempt. The id is the authorization
// request id, stored under a different kind from companion attempts.
func (s *Server) beginBnetLogin(ctx context.Context, requestID string) (string, error) {
	if !s.bnetConfigured() {
		return "", errBnetNotConfigured
	}
	verifier, challenge, err := pkcePair()
	if err != nil {
		return "", err
	}
	state, err := randomToken(16)
	if err != nil {
		return "", err
	}
	if err := s.store.UpsertBnetAttempt(ctx, bnetAttemptRecord{
		Kind: bnetKindOAuth, ID: requestID, Verifier: verifier, State: state, OAuthRequestID: requestID,
	}); err != nil {
		return "", err
	}
	return s.battleNetAuthorizeURL(state, challenge), nil
}

func (s *Server) bnetRedirect() string {
	return strings.TrimRight(s.publicBaseURL, "/") + "/v1/auth/bnet/callback"
}

func (s *Server) bnetCallback(w http.ResponseWriter, r *http.Request) {
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	row, err := s.store.BnetAttemptByState(r.Context(), state)
	if err != nil && !errors.Is(err, ErrNotFound) {
		s.fail(w, err)
		return
	}
	if errors.Is(err, ErrNotFound) || code == "" {
		writeErr(w, http.StatusBadRequest, "invalid", "unknown Battle.net login attempt")
		return
	}
	info, err := s.exchangeBnet(r.Context(), code, row.Verifier)
	if err != nil {
		_ = s.store.SetBnetAttemptError(r.Context(), row.Kind, row.ID, "bnet_unavailable", "Battle.net login failed")
		writeErr(w, http.StatusBadGateway, "bnet_unavailable", "Battle.net login failed")
		return
	}
	player, err := s.store.MergeBattleNet(r.Context(), info.AccountID, info.BattleTag, info.DisplayName)
	if err != nil {
		s.fail(w, err)
		return
	}
	if row.Kind == bnetKindOAuth {
		raw, err := s.store.IssueConsentSession(r.Context(), player.ID)
		if err != nil {
			s.fail(w, err)
			return
		}
		_ = s.store.DeleteBnetAttempt(r.Context(), row.Kind, row.ID)
		http.SetCookie(w, oauthSessionCookie(s.publicBaseURL, raw, r))
		http.Redirect(w, r, "/oauth/authorize?request="+url.QueryEscape(row.OAuthRequestID), http.StatusFound)
		return
	}
	sess, err := s.store.IssueSessionFor(r.Context(), player.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	wire := sessionWire{TokenType: sess.TokenType, SessionToken: sess.SessionToken, ExpiresAt: sess.ExpiresAt, Player: sess.Player}
	if err := s.store.SetBnetAttemptSession(r.Context(), row.Kind, row.ID, wire); err != nil {
		s.fail(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, "<!doctype html><title>Sagalore</title><p>You are logged in. You can close this window.</p>")
}

func (s *Server) bnetPoll(w http.ResponseWriter, r *http.Request) {
	attempt := strings.TrimSpace(r.URL.Query().Get("attempt"))
	row, err := s.store.BnetAttempt(r.Context(), bnetKindCompanion, attempt)
	if errors.Is(err, ErrNotFound) {
		writeErr(w, http.StatusNotFound, "not_found", "unknown attempt")
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	if row.ErrCode != "" {
		writeErr(w, http.StatusBadGateway, row.ErrCode, row.ErrMsg)
		return
	}
	if row.Session == nil {
		writeJSON(w, http.StatusAccepted, map[string]string{"status": "pending"})
		return
	}
	writeJSON(w, http.StatusOK, row.Session)
}

func (s *Server) devLogin(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(s.devToken) == "" || subtleConstantTimeString(bearer(r), s.devToken) != 1 {
		writeErr(w, http.StatusUnauthorized, "unauthorized", "invalid dev token")
		return
	}
	var body struct {
		AccountID   string `json:"bnet_account_id"`
		BattleTag   string `json:"battletag"`
		DisplayName string `json:"display_name"`
	}
	if r.ContentLength != 0 {
		if err := readJSON(r, &body, 1<<16); err != nil {
			writeErr(w, http.StatusBadRequest, "invalid", "bad json")
			return
		}
	}
	if strings.TrimSpace(body.AccountID) == "" {
		body.AccountID = "dev"
	}
	if strings.TrimSpace(body.BattleTag) == "" {
		body.BattleTag = "Player#1234"
	}
	if strings.TrimSpace(body.DisplayName) == "" {
		body.DisplayName = "Player"
	}
	player, err := s.store.MergeBattleNet(r.Context(), body.AccountID, body.BattleTag, body.DisplayName)
	if err != nil {
		s.fail(w, err)
		return
	}
	sess, err := s.store.IssueSessionFor(r.Context(), player.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

type bnetUser struct {
	AccountID   string
	BattleTag   string
	DisplayName string
}

func (s *Server) exchangeBnet(ctx context.Context, code, verifier string) (bnetUser, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", s.bnetRedirect())
	form.Set("code_verifier", verifier)
	flow := s.bnetFlow()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, flow.tokenEndpoint(), strings.NewReader(form.Encode()))
	if err != nil {
		return bnetUser{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(s.bnetClientID, s.bnetClientSecret)
	resp, err := flow.client.Do(req)
	if err != nil {
		return bnetUser{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return bnetUser{}, fmt.Errorf("token status %d", resp.StatusCode)
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.Unmarshal(raw, &tok); err != nil || tok.AccessToken == "" {
		return bnetUser{}, fmt.Errorf("token response")
	}
	ureq, err := http.NewRequestWithContext(ctx, http.MethodGet, flow.userInfoEndpoint(), nil)
	if err != nil {
		return bnetUser{}, err
	}
	ureq.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	uresp, err := flow.client.Do(ureq)
	if err != nil {
		return bnetUser{}, err
	}
	defer uresp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(uresp.Body, 1<<20))
	if uresp.StatusCode < 200 || uresp.StatusCode >= 300 {
		return bnetUser{}, fmt.Errorf("userinfo status %d", uresp.StatusCode)
	}
	var info struct {
		ID        json.RawMessage `json:"id"`
		BattleTag string          `json:"battletag"`
		Sub       string          `json:"sub"`
	}
	if err := json.Unmarshal(body, &info); err != nil {
		return bnetUser{}, err
	}
	id := strings.Trim(string(info.ID), `"`)
	if id == "" {
		id = strings.TrimSpace(info.Sub)
	}
	name := info.BattleTag
	if i := strings.Index(name, "#"); i > 0 {
		name = name[:i]
	}
	return bnetUser{AccountID: id, BattleTag: info.BattleTag, DisplayName: name}, nil
}

func pkcePair() (verifier, challenge string, err error) {
	verifier, err = randomToken(32)
	if err != nil {
		return "", "", err
	}
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

func randomToken(n int) (string, error) {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}
