package main

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"html/template"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/sagalore/sagalore/internal/contract"
)

const oauthRegisterPerHour = 20

const oauthCookieName = "sagalore_oauth"

// OAuth 2.1 authorization server. Any registered client can ask a player to
// approve access. The access token is a gst_ credential, so the existing API
// accepts it. Refresh tokens use the gsr_ prefix and are not API keys.

func (s *Server) oauthIssuer() string {
	return strings.TrimRight(strings.TrimSpace(s.publicBaseURL), "/")
}

func (s *Server) resourceMetadataURL() string {
	issuer := s.oauthIssuer()
	if issuer == "" {
		return ""
	}
	return issuer + "/.well-known/oauth-protected-resource"
}

func (s *Server) oauthOptions(w http.ResponseWriter, r *http.Request) {
	s.oauthCORS(w, r)
}

func (s *Server) oauthCORS(w http.ResponseWriter, r *http.Request) bool {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Accept")
	w.Header().Set("Access-Control-Max-Age", "86400")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}

func (s *Server) oauthASMetadata(w http.ResponseWriter, r *http.Request) {
	if s.oauthCORS(w, r) {
		return
	}
	issuer := s.oauthIssuer()
	if issuer == "" {
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "PUBLIC_BASE_URL is required")
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{
		"issuer":                                         issuer,
		"authorization_endpoint":                         issuer + "/oauth/authorize",
		"token_endpoint":                                 issuer + "/oauth/token",
		"registration_endpoint":                          issuer + "/oauth/register",
		"revocation_endpoint":                            issuer + "/oauth/revoke",
		"response_types_supported":                       []string{"code"},
		"grant_types_supported":                          []string{"authorization_code", "refresh_token"},
		"code_challenge_methods_supported":               []string{"S256"},
		"token_endpoint_auth_methods_supported":          []string{"none", "client_secret_basic", "client_secret_post"},
		"revocation_endpoint_auth_methods_supported":     []string{"none", "client_secret_basic", "client_secret_post"},
		"scopes_supported":                               []string{oauthScopePlayer},
		"authorization_response_iss_parameter_supported": true,
	})
}

func (s *Server) oauthResourceMetadata(w http.ResponseWriter, r *http.Request) {
	if s.oauthCORS(w, r) {
		return
	}
	issuer := s.oauthIssuer()
	if issuer == "" {
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "PUBLIC_BASE_URL is required")
		return
	}
	resource := issuer
	if rest := strings.Trim(r.PathValue("rest"), "/"); rest != "" {
		resource = issuer + "/" + rest
	}
	w.Header().Set("Cache-Control", "public, max-age=3600")
	writeJSON(w, http.StatusOK, map[string]any{
		"resource":                 resource,
		"authorization_servers":    []string{issuer},
		"bearer_methods_supported": []string{"header"},
		"scopes_supported":         []string{oauthScopePlayer},
		"resource_name":            "Sagalore",
	})
}

func (s *Server) oauthRegister(w http.ResponseWriter, r *http.Request) {
	if s.oauthCORS(w, r) {
		return
	}
	_ = s.store.CleanupOAuth(r.Context())
	allowed, err := s.store.AllowOAuthRegistration(r.Context(), clientIP(r))
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	if !allowed {
		w.Header().Set("Retry-After", "3600")
		writeOAuthError(w, http.StatusTooManyRequests, "slow_down", "too many client registrations")
		return
	}
	var body struct {
		RedirectURIs            []string `json:"redirect_uris"`
		ClientName              string   `json:"client_name"`
		GrantTypes              []string `json:"grant_types"`
		ResponseTypes           []string `json:"response_types"`
		TokenEndpointAuthMethod string   `json:"token_endpoint_auth_method"`
		Scope                   string   `json:"scope"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&body); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "invalid json")
		return
	}
	redirects, errCode, errDesc := normalizeRedirects(body.RedirectURIs)
	if errCode != "" {
		writeOAuthError(w, http.StatusBadRequest, errCode, errDesc)
		return
	}
	name := oauthClientName(body.ClientName, redirects[0])
	if strings.TrimSpace(body.ClientName) != "" && name == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "client_name is too long")
		return
	}
	if name == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "client_name is required")
		return
	}
	grants, ok := normalizeOAuthGrants(body.GrantTypes)
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "grant_types are not supported")
		return
	}
	responses, ok := normalizeOAuthResponses(body.ResponseTypes)
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "response_types are not supported")
		return
	}
	method, ok := normalizeOAuthAuthMethod(body.TokenEndpointAuthMethod)
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "token_endpoint_auth_method is not supported")
		return
	}
	scope, ok := normalizeOAuthScope(body.Scope)
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_client_metadata", "scope is not supported")
		return
	}
	secret := ""
	if method != "none" {
		var err error
		secret, err = newToken("gck_")
		if err != nil {
			s.writeOAuthStoreErr(w, err)
			return
		}
	}
	rec, err := s.store.CreateOAuthClient(r.Context(), oauthClientDraft{
		Name: name, Redirects: redirects, Grants: grants, Responses: responses,
		AuthMethod: method, Scope: scope, Secret: secret,
	})
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	out := map[string]any{
		"client_id":                  rec.ClientID,
		"client_id_issued_at":        rec.Created.Unix(),
		"client_name":                rec.Name,
		"redirect_uris":              rec.Redirects,
		"grant_types":                rec.Grants,
		"response_types":             rec.Responses,
		"token_endpoint_auth_method": rec.AuthMethod,
		"scope":                      rec.Scope,
	}
	if secret != "" {
		out["client_secret"] = secret
		out["client_secret_expires_at"] = 0
	}
	writeOAuthJSON(w, http.StatusCreated, out)
}

func (s *Server) oauthAuthorize(w http.ResponseWriter, r *http.Request) {
	if s.oauthIssuer() == "" {
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "PUBLIC_BASE_URL is required")
		return
	}
	_ = s.store.CleanupOAuth(r.Context())
	if requestID := strings.TrimSpace(r.URL.Query().Get("request")); requestID != "" {
		if validUUID(requestID) != nil {
			s.renderOAuthRestart(w)
			return
		}
		s.oauthResume(w, r, requestID)
		return
	}
	q := r.URL.Query()
	clientID := strings.TrimSpace(q.Get("client_id"))
	redirectURI := q.Get("redirect_uri")
	if clientID == "" || len(clientID) > 200 {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "client_id is required")
		return
	}
	client, err := s.store.OAuthClient(r.Context(), clientID)
	if errors.Is(err, ErrNotFound) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "unknown client")
		return
	}
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	if !validOAuthRedirect(redirectURI) || !containsString(client.Redirects, redirectURI) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri is not registered")
		return
	}
	state := q.Get("state")
	if state == "" || len(state) > 512 {
		oauthRedirectError(w, r, redirectURI, "", s.oauthIssuer(), "invalid_request", "state is required")
		return
	}
	if q.Get("response_type") != "code" {
		oauthRedirectError(w, r, redirectURI, state, s.oauthIssuer(), "unsupported_response_type", "response_type must be code")
		return
	}
	if mode := q.Get("response_mode"); mode != "" && mode != "query" {
		oauthRedirectError(w, r, redirectURI, state, s.oauthIssuer(), "invalid_request", "response_mode must be query")
		return
	}
	challenge := q.Get("code_challenge")
	if q.Get("code_challenge_method") != "S256" || !validPKCEChallenge(challenge) {
		oauthRedirectError(w, r, redirectURI, state, s.oauthIssuer(), "invalid_request", "PKCE S256 is required")
		return
	}
	scope, ok := normalizeOAuthScope(q.Get("scope"))
	if !ok {
		oauthRedirectError(w, r, redirectURI, state, s.oauthIssuer(), "invalid_scope", "scope is not supported")
		return
	}
	resource := q.Get("resource")
	if resource != "" && !oauthResourceAllowed(s.oauthIssuer(), resource) {
		oauthRedirectError(w, r, redirectURI, state, s.oauthIssuer(), "invalid_target", "resource is not this API")
		return
	}
	requestID, err := s.store.CreateOAuthRequest(r.Context(), client.ClientID, redirectURI, state, challenge, scope, resource)
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	if _, _, loggedIn := s.playerFromOAuthCookie(r); !loggedIn {
		s.redirectOAuthToBattleNet(w, r, requestID, false)
		return
	}
	s.oauthResume(w, r, requestID)
}

func (s *Server) oauthResume(w http.ResponseWriter, r *http.Request, requestID string) {
	row, expired, err := s.store.OAuthRequest(r.Context(), requestID)
	if errors.Is(err, ErrNotFound) {
		s.renderOAuthRestart(w)
		return
	}
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	if expired {
		_ = s.store.DeleteOAuthRequest(r.Context(), requestID)
		oauthRedirectError(w, r, row.RedirectURI, row.State, s.oauthIssuer(), "invalid_request", "authorization request expired")
		return
	}
	client, err := s.store.OAuthClient(r.Context(), row.ClientID)
	if err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "unknown client")
		return
	}
	player, consent, ok := s.playerFromOAuthCookie(r)
	if !ok {
		s.redirectOAuthToBattleNet(w, r, requestID, false)
		return
	}
	s.showConsent(w, r, client, player, row, consent)
}

func (s *Server) oauthAuthorizeDecision(w http.ResponseWriter, r *http.Request) {
	if s.oauthIssuer() == "" {
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "PUBLIC_BASE_URL is required")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form")
		return
	}
	if !oauthOriginOK(s.oauthIssuer(), r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site")) {
		writeOAuthError(w, http.StatusForbidden, "invalid_request", "origin is not allowed")
		return
	}
	requestID := strings.TrimSpace(r.PostForm.Get("request"))
	if validUUID(requestID) != nil {
		s.renderOAuthRestart(w)
		return
	}
	row, expired, err := s.store.OAuthRequest(r.Context(), requestID)
	if errors.Is(err, ErrNotFound) {
		s.replayApprove(w, r, requestID)
		return
	}
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	if expired {
		_ = s.store.DeleteOAuthRequest(r.Context(), requestID)
		s.oauthConsentError(w, r, row.RedirectURI, row.State, "invalid_request", "authorization request expired")
		return
	}
	player, consent, ok := s.playerFromOAuthCookie(r)
	if !ok {
		s.redirectOAuthToBattleNet(w, r, requestID, true)
		return
	}
	if !oauthCSRFOK(row, consent, r.PostForm.Get("csrf")) {
		writeOAuthError(w, http.StatusForbidden, "invalid_request", "csrf token does not match this sign-in")
		return
	}
	switch r.PostForm.Get("decision") {
	case "deny":
		if err := s.store.DeleteOAuthRequest(r.Context(), requestID); err != nil && !errors.Is(err, ErrNotFound) {
			s.writeOAuthStoreErr(w, err)
			return
		}
		s.oauthConsentError(w, r, row.RedirectURI, row.State, "access_denied", "the request was denied")
	case "approve":
		raw, issued, err := s.store.IssueOAuthCode(r.Context(), requestID, player.ID)
		if errors.Is(err, errOAuthRequestExpired) {
			s.oauthConsentError(w, r, issued.RedirectURI, issued.State, "invalid_request", "authorization request expired")
			return
		}
		if errors.Is(err, ErrNotFound) {
			s.replayApprove(w, r, requestID)
			return
		}
		if err != nil {
			s.writeOAuthStoreErr(w, err)
			return
		}
		s.oauthConsentCode(w, r, issued.RedirectURI, issued.State, raw)
	default:
		client, err := s.store.OAuthClient(r.Context(), row.ClientID)
		if err != nil {
			writeOAuthError(w, http.StatusBadRequest, "invalid_request", "unknown client")
			return
		}
		s.showConsent(w, r, client, player, row, consent)
	}
}

func (s *Server) showConsent(w http.ResponseWriter, r *http.Request, client oauthClientRecord, player contract.Player, row oauthRequestRow, consent string) {
	csrf, err := randomToken(32)
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	if err := s.store.BindOAuthConsent(r.Context(), row.ID, consent, csrf); err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	s.renderConsent(w, consentView{
		ClientName: client.Name, Who: oauthWho(player), RedirectURI: row.RedirectURI, RequestID: row.ID, CSRF: csrf,
	})
}

func (s *Server) redirectOAuthToBattleNet(w http.ResponseWriter, r *http.Request, requestID string, handoff bool) {
	loc, err := s.beginBnetLogin(r.Context(), requestID)
	if errors.Is(err, errBnetNotConfigured) {
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "BNET_CLIENT_ID and PUBLIC_BASE_URL are required")
		return
	}
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	if handoff {
		s.oauthHandoff(w, r, loc)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, loc, http.StatusFound)
}

// replayApprove repeats an Allow that already issued a code. The same consent
// session and CSRF token get the same code and state until the replay window
// ends. A different CSRF token is rejected. Anything else is an HTML page.
func (s *Server) replayApprove(w http.ResponseWriter, r *http.Request, requestID string) {
	if r.PostForm.Get("decision") != "approve" {
		s.renderOAuthRestart(w)
		return
	}
	replay, err := s.store.OAuthApproveReplay(r.Context(), requestID)
	if errors.Is(err, ErrNotFound) {
		s.renderOAuthRestart(w)
		return
	}
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	_, consent, ok := s.playerFromOAuthCookie(r)
	if !ok || !oauthReplayCSRF(replay, consent, r.PostForm.Get("csrf")) {
		writeOAuthError(w, http.StatusForbidden, "invalid_request", "csrf token does not match this sign-in")
		return
	}
	s.oauthConsentCode(w, r, replay.RedirectURI, replay.State, replay.Code)
}

func (s *Server) oauthToken(w http.ResponseWriter, r *http.Request) {
	if s.oauthCORS(w, r) {
		return
	}
	_ = s.store.CleanupOAuth(r.Context())
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	client, ok := s.oauthAuthenticateClient(w, r)
	if !ok {
		return
	}
	switch r.PostForm.Get("grant_type") {
	case "authorization_code":
		s.oauthCodeGrant(w, r, client)
	case "refresh_token":
		s.oauthRefreshGrant(w, r, client)
	default:
		writeOAuthError(w, http.StatusBadRequest, "unsupported_grant_type", "grant_type is not supported")
	}
}

func (s *Server) oauthCodeGrant(w http.ResponseWriter, r *http.Request, client oauthClientRecord) {
	if !containsString(client.Grants, "authorization_code") {
		writeOAuthError(w, http.StatusBadRequest, "unauthorized_client", "authorization_code is not allowed for this client")
		return
	}
	code := r.PostForm.Get("code")
	redirectURI := r.PostForm.Get("redirect_uri")
	verifier := r.PostForm.Get("code_verifier")
	if code == "" || redirectURI == "" || verifier == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "code, redirect_uri, and code_verifier are required")
		return
	}
	if !containsString(client.Redirects, redirectURI) {
		writeOAuthError(w, http.StatusBadRequest, "invalid_grant", "redirect_uri does not match")
		return
	}
	tokens, err := s.store.RedeemOAuthCode(r.Context(), client, oauthCodeGrant{
		Code: code, RedirectURI: redirectURI, Verifier: verifier,
		Resource: r.PostForm.Get("resource"), Issuer: s.oauthIssuer(),
	})
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	writeOAuthTokens(w, tokens)
}

func (s *Server) oauthRefreshGrant(w http.ResponseWriter, r *http.Request, client oauthClientRecord) {
	if !containsString(client.Grants, "refresh_token") {
		writeOAuthError(w, http.StatusBadRequest, "unauthorized_client", "refresh_token is not allowed for this client")
		return
	}
	refresh := r.PostForm.Get("refresh_token")
	if refresh == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "refresh_token is required")
		return
	}
	if strings.TrimSpace(r.PostForm.Get("scope")) != "" {
		if _, ok := normalizeOAuthScope(r.PostForm.Get("scope")); !ok {
			writeOAuthError(w, http.StatusBadRequest, "invalid_scope", "scope is not supported")
			return
		}
	}
	tokens, err := s.store.RotateOAuthRefresh(r.Context(), client, oauthRefreshGrant{
		Refresh: refresh, Resource: r.PostForm.Get("resource"), Issuer: s.oauthIssuer(),
	})
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	writeOAuthTokens(w, tokens)
}

func (s *Server) oauthRevoke(w http.ResponseWriter, r *http.Request) {
	if s.oauthCORS(w, r) {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	client, ok := s.oauthAuthenticateClient(w, r)
	if !ok {
		return
	}
	token := r.PostForm.Get("token")
	if token == "" {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "token is required")
		return
	}
	if err := s.store.RevokeOAuthToken(r.Context(), client.ClientID, token, r.PostForm.Get("token_type_hint")); err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	w.WriteHeader(http.StatusOK)
}

func (s *Server) oauthAuthenticateClient(w http.ResponseWriter, r *http.Request) (oauthClientRecord, bool) {
	if err := r.ParseForm(); err != nil {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "invalid form")
		return oauthClientRecord{}, false
	}
	basicID, basicSecret, hasBasic := r.BasicAuth()
	clientID := strings.TrimSpace(r.PostForm.Get("client_id"))
	if hasBasic {
		if clientID == "" {
			clientID = basicID
		} else if clientID != basicID {
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
			return oauthClientRecord{}, false
		}
	}
	if clientID == "" {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return oauthClientRecord{}, false
	}
	client, err := s.store.OAuthClient(r.Context(), clientID)
	if err != nil {
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return oauthClientRecord{}, false
	}
	switch client.AuthMethod {
	case "none":
		if r.PostForm.Get("client_secret") != "" || (hasBasic && basicSecret != "") {
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "public client does not use a secret")
			return oauthClientRecord{}, false
		}
	case "client_secret_basic":
		if !hasBasic || client.SecretHash == "" || subtleConstantTimeString(hashKey(basicSecret), client.SecretHash) != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="oauth"`)
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
			return oauthClientRecord{}, false
		}
	case "client_secret_post":
		posted := r.PostForm.Get("client_secret")
		if posted == "" || client.SecretHash == "" || subtleConstantTimeString(hashKey(posted), client.SecretHash) != 1 {
			writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
			return oauthClientRecord{}, false
		}
	default:
		writeOAuthError(w, http.StatusUnauthorized, "invalid_client", "client authentication failed")
		return oauthClientRecord{}, false
	}
	return client, true
}

func (s *Server) playerFromOAuthCookie(r *http.Request) (contract.Player, string, bool) {
	c, err := r.Cookie(oauthCookieName)
	if err != nil || !strings.HasPrefix(c.Value, oauthConsentPref) {
		return contract.Player{}, "", false
	}
	p, err := s.store.PlayerByConsentSession(r.Context(), c.Value)
	if err != nil {
		return contract.Player{}, "", false
	}
	return p, c.Value, true
}

func oauthSessionCookie(publicBase, token string, r *http.Request) *http.Cookie {
	secure := r.TLS != nil || strings.HasPrefix(strings.ToLower(publicBase), "https://")
	return &http.Cookie{
		Name:     oauthCookieName,
		Value:    token,
		Path:     "/oauth",
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   int(oauthConsentTTL.Seconds()),
	}
}

func (s *Server) renderConsent(w http.ResponseWriter, view consentView) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	// same-origin keeps the consent POST's Origin equal to the issuer.
	// no-referrer makes browsers send Origin: null, which fails the check below.
	w.Header().Set("Referrer-Policy", "same-origin")
	// form-action lists the client origin because Chromium applies that
	// directive to every hop of a form POST, including a cross-origin 302.
	// The decision still returns a same-origin 303 so the chain never has to.
	w.Header().Set("Content-Security-Policy", consentCSP(view.RedirectURI))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	if err := consentTmpl.Execute(w, view); err != nil {
		slog.Error("oauth consent", "err", err)
	}
}

func oauthCSRFOK(row oauthRequestRow, consent, csrf string) bool {
	return oauthHashesOK(row.ConsentSessionHash, row.CSRFHash, consent, csrf)
}

func oauthReplayCSRF(replay oauthApproveReplay, consent, csrf string) bool {
	return oauthHashesOK(replay.ConsentSessionHash, replay.CSRFHash, consent, csrf)
}

func oauthHashesOK(consentHash, csrfHash, consent, csrf string) bool {
	if consentHash == "" || csrfHash == "" || consent == "" || csrf == "" {
		return false
	}
	return subtleConstantTimeString(hashKey(consent), consentHash) == 1 &&
		subtleConstantTimeString(hashKey(csrf), csrfHash) == 1
}

func consentCSP(redirectURI string) string {
	action := "form-action 'self'"
	if src, ok := oauthFormActionSource(redirectURI); ok {
		action += " " + src
	}
	return "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; " + action + "; base-uri 'none'"
}

// oauthFormActionSource is the CSP source for a registered redirect URI.
// http and https use the origin, including a non-default port. Any other
// allowed scheme is a scheme source so a private-use URI still matches.
func oauthFormActionSource(redirectURI string) (string, bool) {
	u, err := url.Parse(redirectURI)
	if err != nil || u.Scheme == "" || u.Fragment != "" || u.User != nil {
		return "", false
	}
	scheme := strings.ToLower(u.Scheme)
	if !oauthScheme.MatchString(scheme) {
		return "", false
	}
	switch scheme {
	case "javascript", "data", "file", "blob", "vbscript", "about":
		return "", false
	case "http", "https":
		if u.Host == "" || strings.ContainsAny(u.Host, " \t;,'\"\\") {
			return "", false
		}
		return scheme + "://" + u.Host, true
	default:
		return scheme + ":", true
	}
}

// oauthOriginOK allows a consent decision from this issuer. A same-origin form
// can omit Origin or send the literal "null" when a referrer policy withheld it.
// Sec-Fetch-Site: same-origin covers that case. The CSRF token is checked separately.
func oauthOriginOK(issuer, origin, secFetchSite string) bool {
	if issuer == "" {
		return false
	}
	if origin == "" || origin == "null" {
		return secFetchSite == "same-origin"
	}
	got, err := url.Parse(origin)
	want, err2 := url.Parse(issuer)
	if err != nil || err2 != nil || got.Host == "" || want.Host == "" {
		return false
	}
	return strings.EqualFold(got.Scheme, want.Scheme) && strings.EqualFold(got.Host, want.Host)
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		part := strings.TrimSpace(strings.Split(fwd, ",")[0])
		if net.ParseIP(part) != nil {
			return part
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || host == "" {
		return r.RemoteAddr
	}
	return host
}

func oauthWho(p contract.Player) string {
	if strings.TrimSpace(p.BattleTag) != "" {
		return p.BattleTag
	}
	if strings.TrimSpace(p.DisplayName) != "" {
		return p.DisplayName
	}
	return "your account"
}

func writeOAuthError(w http.ResponseWriter, status int, code, desc string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, status, map[string]string{"error": code, "error_description": desc})
}

func writeOAuthJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Pragma", "no-cache")
	writeJSON(w, status, v)
}

func writeOAuthTokens(w http.ResponseWriter, tokens oauthTokens) {
	resp := map[string]any{
		"access_token": tokens.AccessToken,
		"token_type":   "Bearer",
		"expires_in":   tokens.ExpiresIn,
		"scope":        tokens.Scope,
	}
	if tokens.RefreshToken != "" {
		resp["refresh_token"] = tokens.RefreshToken
	}
	if tokens.Resource != "" {
		resp["resource"] = tokens.Resource
	}
	writeOAuthJSON(w, http.StatusOK, resp)
}

func (s *Server) writeOAuthStoreErr(w http.ResponseWriter, err error) {
	var f *oauthFailure
	if errors.As(err, &f) {
		writeOAuthError(w, oauthHTTPStatus(f.Code), f.Code, f.Desc)
		return
	}
	slog.Error("oauth", "err", err)
	writeOAuthError(w, http.StatusInternalServerError, "server_error", "something went wrong")
}

func oauthHTTPStatus(code string) int {
	switch code {
	case "invalid_client":
		return http.StatusUnauthorized
	default:
		return http.StatusBadRequest
	}
}

func oauthRedirectError(w http.ResponseWriter, r *http.Request, redirectURI, state, issuer, code, desc string) {
	target, ok := oauthErrorURL(redirectURI, state, issuer, code, desc)
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, code, desc)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) oauthConsentError(w http.ResponseWriter, r *http.Request, redirectURI, state, code, desc string) {
	target, ok := oauthErrorURL(redirectURI, state, s.oauthIssuer(), code, desc)
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, code, desc)
		return
	}
	s.oauthHandoff(w, r, target)
}

func (s *Server) oauthConsentCode(w http.ResponseWriter, r *http.Request, redirectURI, state, code string) {
	target, ok := oauthCodeURL(redirectURI, state, s.oauthIssuer(), code)
	if !ok {
		writeOAuthError(w, http.StatusBadRequest, "invalid_request", "redirect_uri is invalid")
		return
	}
	s.oauthHandoff(w, r, target)
}

func oauthErrorURL(redirectURI, state, issuer, code, desc string) (string, bool) {
	q := url.Values{}
	q.Set("error", code)
	q.Set("error_description", desc)
	return oauthRedirectURL(redirectURI, state, issuer, q)
}

func oauthCodeURL(redirectURI, state, issuer, code string) (string, bool) {
	q := url.Values{}
	q.Set("code", code)
	return oauthRedirectURL(redirectURI, state, issuer, q)
}

func oauthRedirectURL(redirectURI, state, issuer string, extra url.Values) (string, bool) {
	u, err := url.Parse(redirectURI)
	if err != nil || u.Scheme == "" {
		return "", false
	}
	q := u.Query()
	for key, values := range extra {
		for _, value := range values {
			q.Set(key, value)
		}
	}
	if state != "" {
		q.Set("state", state)
	}
	if issuer != "" {
		q.Set("iss", issuer)
	}
	u.RawQuery = q.Encode()
	return u.String(), true
}

// oauthHandoff answers a consent POST with 303 to a same-origin page. Chromium
// enforces form-action on the redirect chain, so a 302 from this POST to the
// client's redirect URI is dropped and Allow looks like it did nothing.
func (s *Server) oauthHandoff(w http.ResponseWriter, r *http.Request, target string) {
	if s.oauthIssuer() == "" {
		writeOAuthError(w, http.StatusServiceUnavailable, "server_error", "PUBLIC_BASE_URL is required")
		return
	}
	ticket, err := newToken("goh_")
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	if err := s.store.PutOAuthHandoff(r.Context(), ticket, target); err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	loc := s.oauthIssuer() + "/oauth/continue?ticket=" + url.QueryEscape(ticket)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	http.Redirect(w, r, loc, http.StatusSeeOther)
}

func (s *Server) oauthContinue(w http.ResponseWriter, r *http.Request) {
	target, err := s.store.OAuthHandoff(r.Context(), r.URL.Query().Get("ticket"))
	if errors.Is(err, ErrNotFound) {
		s.renderOAuthRestart(w)
		return
	}
	if err != nil {
		s.writeOAuthStoreErr(w, err)
		return
	}
	writeOAuthPage(w, http.StatusOK, oauthStaticCSP, handoffTmpl, handoffView{Target: template.URL(target)})
}

func (s *Server) renderOAuthRestart(w http.ResponseWriter) {
	writeOAuthPage(w, http.StatusBadRequest, oauthStaticCSP, restartTmpl, nil)
}

const oauthStaticCSP = "default-src 'none'; style-src 'unsafe-inline'; img-src 'self'; form-action 'none'; base-uri 'none'"

func writeOAuthPage(w http.ResponseWriter, status int, csp string, tmpl *template.Template, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", csp)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)
	if err := tmpl.Execute(w, data); err != nil {
		slog.Error("oauth page", "err", err)
	}
}

func normalizeRedirects(in []string) ([]string, string, string) {
	if len(in) == 0 {
		return nil, "invalid_redirect_uri", "redirect_uris is required"
	}
	if len(in) > 10 {
		return nil, "invalid_redirect_uri", "too many redirect_uris"
	}
	var out []string
	seen := map[string]bool{}
	for _, raw := range in {
		if !validOAuthRedirect(raw) {
			return nil, "invalid_redirect_uri", "redirect_uri is not allowed"
		}
		if seen[raw] {
			continue
		}
		seen[raw] = true
		out = append(out, raw)
	}
	if len(out) == 0 {
		return nil, "invalid_redirect_uri", "redirect_uris is required"
	}
	return out, "", ""
}

func oauthClientName(submitted, redirect string) string {
	name := strings.TrimSpace(submitted)
	if name == "" {
		u, err := url.Parse(redirect)
		if err != nil || u.Host == "" {
			name = "Application"
		} else {
			name = u.Host
		}
	}
	if len([]rune(name)) > 80 {
		if strings.TrimSpace(submitted) != "" {
			return ""
		}
		name = string([]rune(name)[:80])
	}
	return name
}

var oauthScheme = regexp.MustCompile(`^[a-z][a-z0-9+.-]{0,31}$`)

func validOAuthRedirect(raw string) bool {
	if raw == "" || raw != strings.TrimSpace(raw) || len(raw) > 1024 || strings.ContainsAny(raw, " \t\r\n\\") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Fragment != "" || u.User != nil {
		return false
	}
	scheme := strings.ToLower(u.Scheme)
	if !oauthScheme.MatchString(scheme) {
		return false
	}
	switch scheme {
	case "https":
		return u.Hostname() != ""
	case "http":
		host := strings.ToLower(u.Hostname())
		return host == "localhost" || host == "127.0.0.1" || host == "::1"
	case "javascript", "data", "file", "blob", "vbscript", "about":
		return false
	default:
		return u.Host != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/")
	}
}

func normalizeOAuthGrants(in []string) ([]string, bool) {
	if len(in) == 0 {
		return []string{"authorization_code", "refresh_token"}, true
	}
	var out []string
	seen := map[string]bool{}
	hasCode := false
	for _, g := range in {
		g = strings.TrimSpace(g)
		if g != "authorization_code" && g != "refresh_token" {
			return nil, false
		}
		if seen[g] {
			continue
		}
		seen[g] = true
		if g == "authorization_code" {
			hasCode = true
		}
		out = append(out, g)
	}
	if !hasCode {
		return nil, false
	}
	return out, true
}

func normalizeOAuthResponses(in []string) ([]string, bool) {
	if len(in) == 0 {
		return []string{"code"}, true
	}
	if len(in) != 1 || strings.TrimSpace(in[0]) != "code" {
		return nil, false
	}
	return []string{"code"}, true
}

func normalizeOAuthAuthMethod(method string) (string, bool) {
	switch strings.TrimSpace(method) {
	case "", "none":
		return "none", true
	case "client_secret_basic", "client_secret_post":
		return strings.TrimSpace(method), true
	default:
		return "", false
	}
}

func normalizeOAuthScope(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return oauthScopePlayer, true
	}
	seen := false
	for _, part := range strings.Fields(raw) {
		if part != oauthScopePlayer {
			return "", false
		}
		seen = true
	}
	if !seen {
		return "", false
	}
	return oauthScopePlayer, true
}

func oauthResourceAllowed(issuer, resource string) bool {
	issuer = strings.TrimRight(strings.TrimSpace(issuer), "/")
	if issuer == "" || resource == "" {
		return false
	}
	switch strings.TrimRight(resource, "/") {
	case issuer, issuer + "/v1":
		return true
	default:
		return false
	}
}

func oauthResourceMatch(stored, requested, issuer string) bool {
	if requested != "" && !oauthResourceAllowed(issuer, requested) {
		return false
	}
	if stored == "" || requested == "" {
		return true
	}
	return strings.TrimRight(stored, "/") == strings.TrimRight(requested, "/")
}

func containsString(list []string, want string) bool {
	for _, item := range list {
		if item == want {
			return true
		}
	}
	return false
}

func validPKCEChallenge(s string) bool {
	if len(s) != 43 {
		return false
	}
	for _, r := range s {
		if !isPKCEChallengeChar(r) {
			return false
		}
	}
	return true
}

func validPKCEVerifier(s string) bool {
	if len(s) < 43 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !isPKCEVerifierChar(r) {
			return false
		}
	}
	return true
}

func isPKCEChallengeChar(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_'
}

func isPKCEVerifierChar(r rune) bool {
	return isPKCEChallengeChar(r) || r == '.' || r == '~'
}

func pkceS256(verifier, challenge string) bool {
	if !validPKCEVerifier(verifier) || !validPKCEChallenge(challenge) {
		return false
	}
	sum := sha256.Sum256([]byte(verifier))
	got := base64.RawURLEncoding.EncodeToString(sum[:])
	return subtleConstantTimeString(got, challenge) == 1
}
