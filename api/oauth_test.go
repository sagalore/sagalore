package main

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestOAuthRedirectRules(t *testing.T) {
	ok := []string{
		"https://example.com/callback",
		"https://example.com",
		"http://127.0.0.1/callback",
		"http://127.0.0.1:43821/callback",
		"http://localhost:3000/cb",
		"http://[::1]/cb",
		"com.example.app://callback",
	}
	for _, raw := range ok {
		if !validOAuthRedirect(raw) {
			t.Errorf("want allow %s", raw)
		}
	}
	bad := []string{
		"",
		"http://example.com/cb",
		"https://example.com/cb#frag",
		"javascript:alert(1)",
		"data:text/html,hi",
		"file:///tmp/x",
		"https://user:pass@example.com/cb",
		"https://example.com/a b",
	}
	for _, raw := range bad {
		if validOAuthRedirect(raw) {
			t.Errorf("want reject %s", raw)
		}
	}
}

func TestOAuthMetadataAndRegistration(t *testing.T) {
	_, ts := oauthServer(t)
	resp, err := http.Get(ts.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var meta map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&meta); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || meta["issuer"] != ts.URL {
		t.Fatalf("metadata %+v %d", meta, resp.StatusCode)
	}
	for _, key := range []string{"authorization_endpoint", "token_endpoint", "registration_endpoint", "revocation_endpoint"} {
		if !strings.HasPrefix(meta[key].(string), ts.URL+"/") {
			t.Fatalf("%s = %v", key, meta[key])
		}
	}
	if methods, _ := meta["code_challenge_methods_supported"].([]any); len(methods) != 1 || methods[0] != "S256" {
		t.Fatalf("pkce %v", meta["code_challenge_methods_supported"])
	}
	if meta["authorization_response_iss_parameter_supported"] != true {
		t.Fatalf("iss flag %v", meta["authorization_response_iss_parameter_supported"])
	}

	resp, err = http.Get(ts.URL + "/.well-known/oauth-protected-resource")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var resource map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&resource); err != nil {
		t.Fatal(err)
	}
	if resource["resource"] != ts.URL {
		t.Fatalf("resource %v", resource["resource"])
	}
	servers, _ := resource["authorization_servers"].([]any)
	if len(servers) != 1 || servers[0] != ts.URL {
		t.Fatalf("authorization_servers %v", resource["authorization_servers"])
	}

	resp, err = http.Get(ts.URL + "/.well-known/oauth-protected-resource/v1")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var v1 map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&v1); err != nil {
		t.Fatal(err)
	}
	if v1["resource"] != ts.URL+"/v1" {
		t.Fatalf("v1 resource %v", v1["resource"])
	}

	req, err := http.NewRequest(http.MethodOptions, ts.URL+"/oauth/register", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent || resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("cors %d %q", resp.StatusCode, resp.Header.Get("Access-Control-Allow-Origin"))
	}

	bad, err := http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(`{"redirect_uris":["http://evil.example/cb"],"client_name":"Nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Body.Close()
	var oauthErr map[string]string
	_ = json.NewDecoder(bad.Body).Decode(&oauthErr)
	if bad.StatusCode != http.StatusBadRequest || oauthErr["error"] != "invalid_redirect_uri" {
		t.Fatalf("bad redirect %+v %d", oauthErr, bad.StatusCode)
	}

	client := registerOAuthClient(t, ts, map[string]any{
		"client_name": "Board",
		"redirect_uris": []string{
			"http://127.0.0.1/callback",
			"com.example.app://callback",
		},
		"application_type": "native",
	})
	if !strings.HasPrefix(client["client_id"].(string), "gsc_") || client["client_secret"] != nil {
		t.Fatalf("public client %+v", client)
	}
	if client["token_endpoint_auth_method"] != "none" || client["scope"] != "player" {
		t.Fatalf("defaults %+v", client)
	}
}

func TestOAuthConsentTokensRefreshAndRevoke(t *testing.T) {
	srv, ts := oauthServer(t)
	_, session := oauthDevSession(t, ts, srv.store)
	redirectURI := "http://127.0.0.1/callback"
	client := registerOAuthClient(t, ts, map[string]any{
		"client_name":   "Board",
		"redirect_uris": []string{redirectURI},
	})
	clientID := client["client_id"].(string)

	unauth, err := http.Get(ts.URL + "/v1/groups")
	if err != nil {
		t.Fatal(err)
	}
	unauth.Body.Close()
	if unauth.StatusCode != http.StatusUnauthorized || !strings.Contains(unauth.Header.Get("WWW-Authenticate"), ts.URL+"/.well-known/oauth-protected-resource") {
		t.Fatalf("www-authenticate %d %q", unauth.StatusCode, unauth.Header.Get("WWW-Authenticate"))
	}

	noRedirect := oauthGet(t, ts, "/oauth/authorize?client_id="+url.QueryEscape(clientID)+"&redirect_uri="+url.QueryEscape("http://127.0.0.1/other")+"&response_type=code&state=s&code_challenge="+pkceChallenge(t)+"&code_challenge_method=S256", session, false)
	noRedirect.Body.Close()
	if noRedirect.StatusCode != http.StatusBadRequest || noRedirect.Header.Get("Location") != "" {
		t.Fatalf("unregistered redirect %d %s", noRedirect.StatusCode, noRedirect.Header.Get("Location"))
	}

	verifier, challenge, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	missingState := "/oauth/authorize?client_id=" + url.QueryEscape(clientID) + "&redirect_uri=" + url.QueryEscape(redirectURI) + "&response_type=code&code_challenge=" + challenge + "&code_challenge_method=S256"
	stateResp := oauthGet(t, ts, missingState, "", false)
	stateLoc, _ := url.Parse(stateResp.Header.Get("Location"))
	stateResp.Body.Close()
	if stateResp.StatusCode != http.StatusFound || stateLoc.Query().Get("error") != "invalid_request" {
		t.Fatalf("state %d %s", stateResp.StatusCode, stateResp.Header.Get("Location"))
	}

	code := approveOAuth(t, ts, clientID, redirectURI, session, verifier, challenge, "client-state", ts.URL)
	tokens := exchangeOAuthCode(t, ts, clientID, "", "", redirectURI, code, verifier)
	if !strings.HasPrefix(tokens.AccessToken, "gst_") || !strings.HasPrefix(tokens.RefreshToken, "gsr_") || tokens.ExpiresIn != 3600 || tokens.Scope != "player" {
		t.Fatalf("tokens %+v", tokens)
	}
	if oauthStatus(t, ts, tokens.AccessToken) != http.StatusOK {
		t.Fatal("access token was not accepted")
	}
	if oauthStatus(t, ts, tokens.RefreshToken) != http.StatusUnauthorized {
		t.Fatal("refresh token was accepted as an API key")
	}

	again := tokenForm(t, ts, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {clientID}, "code_verifier": {verifier},
	})
	if again.Error != "invalid_grant" {
		t.Fatalf("replay %+v", again)
	}
	if oauthStatus(t, ts, tokens.AccessToken) != http.StatusUnauthorized {
		t.Fatal("replay did not revoke the access token")
	}

	verifier, challenge, err = pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	code = approveOAuth(t, ts, clientID, redirectURI, session, verifier, challenge, "client-state", "")
	badPKCE := tokenForm(t, ts, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {clientID}, "code_verifier": {verifier + "nope"},
	})
	if badPKCE.Error != "invalid_grant" {
		t.Fatalf("pkce %+v", badPKCE)
	}
	burned := tokenForm(t, ts, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {clientID}, "code_verifier": {verifier},
	})
	if burned.Error != "invalid_grant" {
		t.Fatalf("burned code %+v", burned)
	}

	verifier, challenge, err = pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	code = approveOAuth(t, ts, clientID, redirectURI, session, verifier, challenge, "client-state", ts.URL+"/v1")
	tokens = exchangeOAuthCode(t, ts, clientID, "", "", redirectURI, code, verifier)
	if tokens.Resource != ts.URL+"/v1" {
		t.Fatalf("resource %q", tokens.Resource)
	}
	rotated := tokenForm(t, ts, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {clientID},
	})
	if rotated.Error != "" || rotated.RefreshToken == tokens.RefreshToken || !strings.HasPrefix(rotated.AccessToken, "gst_") {
		t.Fatalf("rotate %+v", rotated)
	}
	if oauthStatus(t, ts, rotated.AccessToken) != http.StatusOK {
		t.Fatal("rotated access token")
	}
	if oauthStatus(t, ts, tokens.AccessToken) != http.StatusOK {
		t.Fatal("rotation revoked the previous access token")
	}
	reused := tokenForm(t, ts, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {clientID},
	})
	if reused.Error != "invalid_grant" {
		t.Fatalf("reuse %+v", reused)
	}
	dead := tokenForm(t, ts, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {rotated.RefreshToken}, "client_id": {clientID},
	})
	if dead.Error != "invalid_grant" {
		t.Fatalf("family %+v", dead)
	}
	if oauthStatus(t, ts, rotated.AccessToken) != http.StatusUnauthorized {
		t.Fatal("reuse left the access token alive")
	}

	verifier, challenge, err = pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	code = approveOAuth(t, ts, clientID, redirectURI, session, verifier, challenge, "client-state", "")
	tokens = exchangeOAuthCode(t, ts, clientID, "", "", redirectURI, code, verifier)
	revokeResp := postForm(t, ts.URL+"/oauth/revoke", url.Values{
		"token": {tokens.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {clientID},
	})
	revokeResp.Body.Close()
	if revokeResp.StatusCode != http.StatusOK {
		t.Fatalf("revoke status %d", revokeResp.StatusCode)
	}
	if oauthStatus(t, ts, tokens.AccessToken) != http.StatusUnauthorized {
		t.Fatal("revoked refresh left the access token alive")
	}
	after := tokenForm(t, ts, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {clientID},
	})
	if after.Error != "invalid_grant" {
		t.Fatalf("refresh after revoke %+v", after)
	}

	unknown := postForm(t, ts.URL+"/oauth/revoke", url.Values{"token": {"gst_missing"}, "client_id": {clientID}})
	unknown.Body.Close()
	if unknown.StatusCode != http.StatusOK {
		t.Fatalf("unknown revoke %d", unknown.StatusCode)
	}
}

func TestOAuthAccessExpiryAndAccountRevoke(t *testing.T) {
	srv, ts := oauthServer(t)
	apiToken, session := oauthDevSession(t, ts, srv.store)
	redirectURI := "http://127.0.0.1/callback"
	client := registerOAuthClient(t, ts, map[string]any{
		"client_name":   "Board",
		"redirect_uris": []string{redirectURI},
	})
	clientID := client["client_id"].(string)
	verifier, challenge, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	code := approveOAuth(t, ts, clientID, redirectURI, session, verifier, challenge, "s", "")
	tokens := exchangeOAuthCode(t, ts, clientID, "", "", redirectURI, code, verifier)

	var minted struct {
		Token string `json:"token"`
		ID    string `json:"id"`
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/me/tokens", strings.NewReader(`{"label":"manual"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+apiToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(&minted); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated || !strings.HasPrefix(minted.Token, "gst_") {
		t.Fatalf("mint %+v %d", minted, resp.StatusCode)
	}
	var expires *string
	if err := testPool.QueryRow(context.Background(), `SELECT expires_at::text FROM personal_tokens WHERE id=$1`, minted.ID).Scan(&expires); err != nil {
		t.Fatal(err)
	}
	if expires != nil {
		t.Fatalf("manual token expires %v", *expires)
	}
	if oauthStatus(t, ts, minted.Token) != http.StatusOK {
		t.Fatal("manual token")
	}

	if _, err := testPool.Exec(context.Background(), `
		UPDATE personal_tokens SET expires_at = now() - interval '1 minute' WHERE token_hash=$1`, hashKey(tokens.AccessToken)); err != nil {
		t.Fatal(err)
	}
	if oauthStatus(t, ts, tokens.AccessToken) != http.StatusUnauthorized {
		t.Fatal("expired oauth access token still worked")
	}

	verifier, challenge, err = pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	code = approveOAuth(t, ts, clientID, redirectURI, session, verifier, challenge, "s", "")
	tokens = exchangeOAuthCode(t, ts, clientID, "", "", redirectURI, code, verifier)
	var tokenID string
	if err := testPool.QueryRow(context.Background(), `SELECT id::text FROM personal_tokens WHERE token_hash=$1`, hashKey(tokens.AccessToken)).Scan(&tokenID); err != nil {
		t.Fatal(err)
	}
	del, err := http.NewRequest(http.MethodDelete, ts.URL+"/v1/me/tokens/"+tokenID, nil)
	if err != nil {
		t.Fatal(err)
	}
	del.Header.Set("Authorization", "Bearer "+apiToken)
	resp, err = http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete token %d", resp.StatusCode)
	}
	if oauthStatus(t, ts, tokens.AccessToken) != http.StatusUnauthorized {
		t.Fatal("deleted access token still worked")
	}
	stopped := tokenForm(t, ts, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {clientID},
	})
	if stopped.Error != "invalid_grant" {
		t.Fatalf("refresh after account revoke %+v", stopped)
	}
}

func TestOAuthConfidentialClientAndDeny(t *testing.T) {
	srv, ts := oauthServer(t)
	_, session := oauthDevSession(t, ts, srv.store)
	redirectURI := "http://localhost:9/callback"
	client := registerOAuthClient(t, ts, map[string]any{
		"client_name":                "Secret Board",
		"redirect_uris":              []string{redirectURI},
		"token_endpoint_auth_method": "client_secret_basic",
		"grant_types":                []string{"authorization_code", "refresh_token"},
		"response_types":             []string{"code"},
	})
	clientID := client["client_id"].(string)
	secret, _ := client["client_secret"].(string)
	if !strings.HasPrefix(secret, "gck_") || client["client_secret_expires_at"].(float64) != 0 {
		t.Fatalf("secret %+v", client)
	}
	deny := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{"request": {"not-a-uuid"}, "decision": {"deny"}}, "", ts.URL)
	deny.Body.Close()
	if deny.StatusCode != http.StatusBadRequest {
		t.Fatalf("bogus decision %d", deny.StatusCode)
	}

	verifier, challenge, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	page := oauthGet(t, ts, authorizeQuery(clientID, redirectURI, challenge, "deny-state", ""), session, true)
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	requestID := oauthRequestID(t, string(body))
	denyResp := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {requestID}, "decision": {"deny"}, "csrf": {oauthCSRF(t, string(body))},
	}, session, ts.URL)
	denyLoc := oauthDecisionTarget(t, ts, denyResp)
	if denyLoc.Query().Get("error") != "access_denied" || denyLoc.Query().Get("state") != "deny-state" {
		t.Fatalf("deny %s", denyLoc)
	}

	code := approveOAuth(t, ts, clientID, redirectURI, session, verifier, challenge, "ok", "")
	noSecret := tokenForm(t, ts, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {clientID}, "code_verifier": {verifier},
	})
	if noSecret.Error != "invalid_client" {
		t.Fatalf("missing secret %+v", noSecret)
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/oauth/token", strings.NewReader(url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier},
	}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, "wrong-secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "Basic") {
		t.Fatalf("basic %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	tokens := exchangeOAuthCode(t, ts, clientID, secret, "basic", redirectURI, code, verifier)
	if !strings.HasPrefix(tokens.AccessToken, "gst_") {
		t.Fatalf("confidential %+v", tokens)
	}
	req, err = http.NewRequest(http.MethodPost, ts.URL+"/oauth/revoke", strings.NewReader(url.Values{
		"token": {tokens.AccessToken}, "token_type_hint": {"access_token"},
	}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, secret)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("revoke access %d", resp.StatusCode)
	}
	if oauthStatus(t, ts, tokens.AccessToken) != http.StatusUnauthorized {
		t.Fatal("access token was not revoked")
	}
	req, err = http.NewRequest(http.MethodPost, ts.URL+"/oauth/token", strings.NewReader(url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken},
	}.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.SetBasicAuth(clientID, secret)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var rotated oauthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&rotated); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(rotated.AccessToken, "gst_") {
		t.Fatalf("refresh after access revoke %+v %d", rotated, resp.StatusCode)
	}
}

func TestOAuthBattleNetLogin(t *testing.T) {
	resetDB(t)
	bnet := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/authorize":
			dest, err := url.Parse(r.URL.Query().Get("redirect_uri"))
			if err != nil || r.URL.Query().Get("code_challenge_method") != "S256" {
				http.Error(w, "bad authorize", http.StatusBadRequest)
				return
			}
			q := dest.Query()
			q.Set("code", "bnet-code")
			q.Set("state", r.URL.Query().Get("state"))
			dest.RawQuery = q.Encode()
			http.Redirect(w, r, dest.String(), http.StatusFound)
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"access_token":"bnet-access"}`)
		case "/userinfo":
			if r.Header.Get("Authorization") != "Bearer bnet-access" {
				http.Error(w, "no", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"id":4242,"battletag":"OAuth#4242"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(bnet.Close)

	flow := newBnetFlow()
	flow.authorizeURL = bnet.URL + "/authorize"
	flow.tokenURL = bnet.URL + "/token"
	flow.userInfoURL = bnet.URL + "/userinfo"
	api := &Server{
		store: NewStore(testPool), publicBaseURL: "http://127.0.0.1",
		bnetClientID: "test-id", bnetClientSecret: "test-secret", bnet: flow,
	}
	ts := httptest.NewServer(api.Handler())
	t.Cleanup(ts.Close)
	api.publicBaseURL = ts.URL

	redirectURI := bnet.URL + "/cb"
	client := registerOAuthClient(t, ts, map[string]any{
		"client_name":   "Companion-like",
		"redirect_uris": []string{redirectURI},
	})
	verifier, challenge, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	httpClient := ts.Client()
	httpClient.Jar = jar
	httpClient.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if req.URL.Path == "/cb" {
			return http.ErrUseLastResponse
		}
		if len(via) >= 8 {
			return http.ErrUseLastResponse
		}
		return nil
	}
	authURL := ts.URL + authorizeQuery(client["client_id"].(string), redirectURI, challenge, "bnet-state", "")
	resp, err := httpClient.Get(authURL)
	if err != nil {
		t.Fatal(err)
	}
	html, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(html), "OAuth#4242") || !strings.Contains(string(html), "Allow") {
		t.Fatalf("consent %d %s", resp.StatusCode, html)
	}
	requestID := oauthRequestID(t, string(html))
	poll, err := http.Get(ts.URL + "/v1/auth/bnet/poll?attempt=" + url.QueryEscape(requestID))
	if err != nil {
		t.Fatal(err)
	}
	poll.Body.Close()
	if poll.StatusCode != http.StatusNotFound {
		t.Fatalf("oauth login must not be readable from the companion poll, got %d", poll.StatusCode)
	}

	form := url.Values{"request": {requestID}, "decision": {"approve"}, "csrf": {oauthCSRF(t, string(html))}}
	approveReq, err := http.NewRequest(http.MethodPost, ts.URL+"/oauth/authorize", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	approveReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	approveReq.Header.Set("Origin", ts.URL)
	approve, err := httpClient.Do(approveReq)
	if err != nil {
		t.Fatal(err)
	}
	loc := oauthDecisionTarget(t, ts, approve)
	if loc.Query().Get("state") != "bnet-state" || loc.Query().Get("iss") != ts.URL || loc.Query().Get("code") == "" {
		t.Fatalf("return %s", loc)
	}
	tokens := exchangeOAuthCode(t, ts, client["client_id"].(string), "", "", redirectURI, loc.Query().Get("code"), verifier)
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/sync", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	syncResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer syncResp.Body.Close()
	var sync contract.SyncResponse
	if err := json.NewDecoder(syncResp.Body).Decode(&sync); err != nil {
		t.Fatal(err)
	}
	if syncResp.StatusCode != http.StatusOK || sync.BattleTag != "OAuth#4242" {
		t.Fatalf("sync %+v %d", sync, syncResp.StatusCode)
	}
}

func TestPlayerUnauthorizedOmitsMetadataWithoutIssuer(t *testing.T) {
	resetDB(t)
	ts := httptest.NewServer((&Server{store: NewStore(testPool)}).Handler())
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/v1/groups")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || resp.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("header %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
	bad, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/groups", nil)
	if err != nil {
		t.Fatal(err)
	}
	bad.Header.Set("Authorization", "Bearer gst_missing")
	resp, err = http.DefaultClient.Do(bad)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized || !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`) {
		t.Fatalf("invalid token header %d %q", resp.StatusCode, resp.Header.Get("WWW-Authenticate"))
	}
}

func TestOAuthAccessTokenCannotManageTokens(t *testing.T) {
	srv, ts := oauthServer(t)
	apiToken, consent := oauthDevSession(t, ts, srv.store)
	if oauthStatus(t, ts, consent) != http.StatusUnauthorized {
		t.Fatal("consent session was accepted as an API key")
	}
	redirectURI := "http://127.0.0.1/callback"
	client := registerOAuthClient(t, ts, map[string]any{
		"client_name": "Board", "redirect_uris": []string{redirectURI},
	})
	verifier, challenge, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	code := approveOAuth(t, ts, client["client_id"].(string), redirectURI, consent, verifier, challenge, "s", "")
	tokens := exchangeOAuthCode(t, ts, client["client_id"].(string), "", "", redirectURI, code, verifier)
	if oauthStatus(t, ts, tokens.AccessToken) != http.StatusOK {
		t.Fatal("access token")
	}
	mint, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/me/tokens", strings.NewReader(`{"label":"nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	mint.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	mint.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(mint)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]string
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || body["error"] != "oauth_token" {
		t.Fatalf("mint with oauth token %+v %d", body, resp.StatusCode)
	}
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`) && resp.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("forbidden should not be an invalid-token challenge %q", resp.Header.Get("WWW-Authenticate"))
	}
	del, err := http.NewRequest(http.MethodDelete, ts.URL+"/v1/me/tokens/00000000-0000-0000-0000-000000000000", nil)
	if err != nil {
		t.Fatal(err)
	}
	del.Header.Set("Authorization", "Bearer "+tokens.AccessToken)
	resp, err = http.DefaultClient.Do(del)
	if err != nil {
		t.Fatal(err)
	}
	body = map[string]string{}
	_ = json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden || body["error"] != "oauth_token" {
		t.Fatalf("revoke with oauth token %+v %d", body, resp.StatusCode)
	}
	player, err := srv.store.PlayerByKey(context.Background(), tokens.AccessToken)
	if err != nil || player.OAuthClientID != client["client_id"].(string) {
		t.Fatalf("PlayerByKey oauth client %q %v", player.OAuthClientID, err)
	}
	sessionPlayer, err := srv.store.PlayerByKey(context.Background(), apiToken)
	if err != nil || sessionPlayer.OAuthClientID != "" {
		t.Fatalf("session oauth client %q %v", sessionPlayer.OAuthClientID, err)
	}

	expired := tokens.AccessToken
	if _, err := testPool.Exec(context.Background(), `
		UPDATE personal_tokens SET expires_at = now() - interval '1 minute' WHERE token_hash=$1`, hashKey(expired)); err != nil {
		t.Fatal(err)
	}
	if got := oauthStatus(t, ts, expired); got != http.StatusUnauthorized {
		t.Fatalf("expired status %d", got)
	}
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/groups", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+expired)
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if !strings.Contains(resp.Header.Get("WWW-Authenticate"), `error="invalid_token"`) || !strings.Contains(resp.Header.Get("WWW-Authenticate"), "resource_metadata=") {
		t.Fatalf("expired header %q", resp.Header.Get("WWW-Authenticate"))
	}
}

func TestOAuthConsentCSRF(t *testing.T) {
	srv, ts := oauthServer(t)
	_, consent := oauthDevSession(t, ts, srv.store)
	redirectURI := "http://127.0.0.1/callback"
	client := registerOAuthClient(t, ts, map[string]any{
		"client_name": "Board", "redirect_uris": []string{redirectURI},
	})
	_, challenge, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	page := oauthGet(t, ts, authorizeQuery(client["client_id"].(string), redirectURI, challenge, "csrf-state", ""), consent, true)
	html, _ := io.ReadAll(page.Body)
	page.Body.Close()
	requestID := oauthRequestID(t, string(html))
	csrf := oauthCSRF(t, string(html))

	noCSRF := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {requestID}, "decision": {"approve"},
	}, consent, ts.URL)
	noCSRF.Body.Close()
	if noCSRF.StatusCode != http.StatusForbidden {
		t.Fatalf("missing csrf %d", noCSRF.StatusCode)
	}
	badOrigin := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {requestID}, "decision": {"approve"}, "csrf": {csrf},
	}, consent, "https://evil.example")
	badOrigin.Body.Close()
	if badOrigin.StatusCode != http.StatusForbidden {
		t.Fatalf("bad origin %d", badOrigin.StatusCode)
	}
	other, err := srv.store.IssueConsentSession(context.Background(), playerIDFromConsent(t, srv, consent))
	if err != nil {
		t.Fatal(err)
	}
	otherSession := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {requestID}, "decision": {"approve"}, "csrf": {csrf},
	}, other, ts.URL)
	otherSession.Body.Close()
	if otherSession.StatusCode != http.StatusForbidden {
		t.Fatalf("csrf bound to another session %d", otherSession.StatusCode)
	}
	ok := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {requestID}, "decision": {"deny"}, "csrf": {csrf},
	}, consent, ts.URL)
	loc := oauthDecisionTarget(t, ts, ok)
	if loc.Query().Get("error") != "access_denied" {
		t.Fatalf("matching csrf %s", loc)
	}
}

func TestOAuthConsentOrigin(t *testing.T) {
	srv, ts := oauthServer(t)
	_, consent := oauthDevSession(t, ts, srv.store)
	redirectURI := "http://127.0.0.1/callback"
	client := registerOAuthClient(t, ts, map[string]any{
		"client_name": "Board", "redirect_uris": []string{redirectURI},
	})
	clientID := client["client_id"].(string)

	policy, requestID, csrf := openOAuthConsent(t, ts, clientID, redirectURI, consent)
	if policy == "no-referrer" || policy != "same-origin" {
		t.Fatalf("consent Referrer-Policy %q", policy)
	}

	rejected := []struct{ origin, site string }{
		{"https://evil.example", "cross-site"},
		{"null", "cross-site"},
		{"", "cross-site"},
		{"null", "same-site"},
		{"null", ""},
		{"", ""},
	}
	for _, tc := range rejected {
		resp := postConsentDecision(t, ts.URL+"/oauth/authorize", url.Values{
			"request": {requestID}, "decision": {"approve"}, "csrf": {csrf},
		}, consent, tc.origin, tc.site)
		var body map[string]string
		_ = json.NewDecoder(resp.Body).Decode(&body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || body["error_description"] != "origin is not allowed" {
			t.Fatalf("origin %q site %q %+v %d", tc.origin, tc.site, body, resp.StatusCode)
		}
	}

	noCSRF := postConsentDecision(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {requestID}, "decision": {"approve"},
	}, consent, "null", "same-origin")
	var csrfBody map[string]string
	_ = json.NewDecoder(noCSRF.Body).Decode(&csrfBody)
	noCSRF.Body.Close()
	if noCSRF.StatusCode != http.StatusForbidden || csrfBody["error_description"] != "csrf token does not match this sign-in" {
		t.Fatalf("null origin without csrf %+v %d", csrfBody, noCSRF.StatusCode)
	}

	issuer := postConsentDecision(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {requestID}, "decision": {"approve"}, "csrf": {csrf},
	}, consent, ts.URL, "")
	if issuer.StatusCode == http.StatusForbidden {
		t.Fatalf("issuer origin %d", issuer.StatusCode)
	}
	issuerLoc := oauthDecisionTarget(t, ts, issuer)
	if issuerLoc.Query().Get("code") == "" {
		t.Fatalf("issuer origin %s", issuerLoc)
	}

	for _, origin := range []string{"null", ""} {
		_, requestID, csrf = openOAuthConsent(t, ts, clientID, redirectURI, consent)
		accepted := postConsentDecision(t, ts.URL+"/oauth/authorize", url.Values{
			"request": {requestID}, "decision": {"approve"}, "csrf": {csrf},
		}, consent, origin, "same-origin")
		if accepted.StatusCode == http.StatusForbidden {
			t.Fatalf("origin %q %d", origin, accepted.StatusCode)
		}
		loc := oauthDecisionTarget(t, ts, accepted)
		if loc.Query().Get("code") == "" {
			t.Fatalf("origin %q same-origin %s", origin, loc)
		}
	}
}

func TestOAuthFormActionSource(t *testing.T) {
	cases := []struct{ raw, want string }{
		{"http://127.0.0.1/callback", "http://127.0.0.1"},
		{"http://127.0.0.1:43821/callback", "http://127.0.0.1:43821"},
		{"http://localhost:3000/cb", "http://localhost:3000"},
		{"http://[::1]/cb", "http://[::1]"},
		{"https://example.com/cb", "https://example.com"},
		{"https://example.com:8443/cb", "https://example.com:8443"},
		{"com.example.app://callback", "com.example.app:"},
	}
	for _, tc := range cases {
		got, ok := oauthFormActionSource(tc.raw)
		if !ok || got != tc.want {
			t.Errorf("%s -> %q ok=%v", tc.raw, got, ok)
		}
	}
	if _, ok := oauthFormActionSource("javascript:alert(1)"); ok {
		t.Fatal("javascript was accepted as a form-action source")
	}
}

func TestOAuthConsentHandoff(t *testing.T) {
	srv, ts := oauthServer(t)
	_, consent := oauthDevSession(t, ts, srv.store)
	redirectURI := "http://127.0.0.1/callback"
	appURI := "com.example.app://callback"
	client := registerOAuthClient(t, ts, map[string]any{
		"client_name":   "Board",
		"redirect_uris": []string{redirectURI, appURI},
	})
	clientID := client["client_id"].(string)

	verifier, challenge, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	page := oauthGet(t, ts, authorizeQuery(clientID, redirectURI, challenge, "nav-state", ""), consent, true)
	raw, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("consent %d %s", page.StatusCode, raw)
	}
	csp := page.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "default-src 'none'") || strings.Contains(csp, "script-src") {
		t.Fatalf("csp %s", csp)
	}
	if got := cspFormAction(csp); got != "form-action 'self' http://127.0.0.1" {
		t.Fatalf("form-action %q", csp)
	}

	appPage := oauthGet(t, ts, authorizeQuery(clientID, appURI, pkceChallenge(t), "app-state", ""), consent, true)
	appRaw, _ := io.ReadAll(appPage.Body)
	appPage.Body.Close()
	if got := cspFormAction(appPage.Header.Get("Content-Security-Policy")); got != "form-action 'self' com.example.app:" {
		t.Fatalf("app form-action %q", appPage.Header.Get("Content-Security-Policy"))
	}
	appResp := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {oauthRequestID(t, string(appRaw))}, "decision": {"approve"}, "csrf": {oauthCSRF(t, string(appRaw))},
	}, consent, ts.URL)
	appLoc := oauthDecisionTarget(t, ts, appResp)
	if appLoc.Scheme != "com.example.app" || !strings.HasPrefix(appLoc.Query().Get("code"), "gso_") || appLoc.Query().Get("state") != "app-state" {
		t.Fatalf("app return %s", appLoc)
	}

	requestID := oauthRequestID(t, string(raw))
	csrf := oauthCSRF(t, string(raw))
	approve := func() *url.URL {
		t.Helper()
		resp := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{
			"request": {requestID}, "decision": {"approve"}, "csrf": {csrf},
		}, consent, ts.URL)
		return oauthDecisionTarget(t, ts, resp)
	}
	first := approve()
	code := first.Query().Get("code")
	if !strings.HasPrefix(code, "gso_") || first.Query().Get("state") != "nav-state" || first.Query().Get("iss") != ts.URL {
		t.Fatalf("first %s", first)
	}
	second := approve()
	if second.Query().Get("code") != code || second.Query().Get("state") != "nav-state" {
		t.Fatalf("replay %s want code %s", second, code)
	}
	var codes int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM oauth_codes`).Scan(&codes); err != nil {
		t.Fatal(err)
	}
	if codes != 2 {
		t.Fatalf("issued %d codes", codes)
	}

	wrong := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {requestID}, "decision": {"approve"}, "csrf": {"different-token"},
	}, consent, ts.URL)
	var wrongBody map[string]string
	_ = json.NewDecoder(wrong.Body).Decode(&wrongBody)
	wrong.Body.Close()
	if wrong.StatusCode != http.StatusForbidden || wrongBody["error_description"] != "csrf token does not match this sign-in" {
		t.Fatalf("different csrf %+v %d", wrongBody, wrong.StatusCode)
	}
	other, err := srv.store.IssueConsentSession(context.Background(), playerIDFromConsent(t, srv, consent))
	if err != nil {
		t.Fatal(err)
	}
	otherSession := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {requestID}, "decision": {"approve"}, "csrf": {csrf},
	}, other, ts.URL)
	var otherBody map[string]string
	_ = json.NewDecoder(otherSession.Body).Decode(&otherBody)
	otherSession.Body.Close()
	if otherSession.StatusCode != http.StatusForbidden || otherBody["error_description"] != "csrf token does not match this sign-in" {
		t.Fatalf("different session %+v %d", otherBody, otherSession.StatusCode)
	}

	exchangeOAuthCode(t, ts, clientID, "", "", redirectURI, code, verifier)
	replayed := tokenForm(t, ts, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI},
		"client_id": {clientID}, "code_verifier": {verifier},
	})
	if replayed.Error != "invalid_grant" {
		t.Fatalf("second exchange %+v", replayed)
	}
	again := approve()
	if again.Query().Get("code") != code {
		t.Fatalf("replay after exchange %s", again)
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM oauth_codes`).Scan(&codes); err != nil {
		t.Fatal(err)
	}
	if codes != 2 {
		t.Fatalf("replay issued a new code, count %d", codes)
	}

	if _, err := testPool.Exec(context.Background(), `
		UPDATE oauth_approve_replays SET expires_at = now() - interval '1 minute' WHERE request_id=$1`, requestID); err != nil {
		t.Fatal(err)
	}
	stale := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {requestID}, "decision": {"approve"}, "csrf": {csrf},
	}, consent, ts.URL)
	assertOAuthRestartPage(t, stale)
	unknown := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {"11111111-1111-1111-1111-111111111111"}, "decision": {"approve"}, "csrf": {csrf},
	}, consent, ts.URL)
	assertOAuthRestartPage(t, unknown)
	missing, err := http.Get(ts.URL + "/oauth/continue?ticket=missing")
	if err != nil {
		t.Fatal(err)
	}
	assertOAuthRestartPage(t, missing)
}

func cspFormAction(csp string) string {
	for _, part := range strings.Split(csp, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "form-action") {
			return part
		}
	}
	return ""
}

func assertOAuthRestartPage(t *testing.T, resp *http.Response) {
	t.Helper()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	text := string(body)
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(resp.Header.Get("Content-Type"), "text/html") {
		t.Fatalf("restart %d %s %s", resp.StatusCode, resp.Header.Get("Content-Type"), text)
	}
	if !strings.Contains(text, "Go back to the app and start again.") || strings.Contains(text, "error_description") {
		t.Fatalf("restart body %s", text)
	}
}

func TestOAuthRegisterRateLimitAndCleanup(t *testing.T) {
	srv, ts := oauthServer(t)
	redirect := map[string]any{"client_name": "Flood", "redirect_uris": []string{"https://example.com/cb"}}
	for i := 0; i < oauthRegisterPerHour; i++ {
		registerOAuthClient(t, ts, redirect)
	}
	raw, _ := json.Marshal(redirect)
	resp, err := http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("rate limit %d", resp.StatusCode)
	}

	_, _ = oauthDevSession(t, ts, srv.store)
	var kept string
	if err := testPool.QueryRow(context.Background(), `SELECT client_id FROM oauth_clients LIMIT 1`).Scan(&kept); err != nil {
		t.Fatal(err)
	}
	if _, err := testPool.Exec(context.Background(), `
		UPDATE oauth_clients SET created_at = now() - interval '8 days' WHERE client_id=$1`, kept); err != nil {
		t.Fatal(err)
	}
	tag, err := testPool.Exec(context.Background(), `
		INSERT INTO oauth_codes (code_hash, client_id, player_id, redirect_uri, code_challenge, scope, expires_at)
		SELECT 'expired-code', $1, id, 'https://example.com/cb', repeat('a', 43), 'player', now() - interval '1 minute'
		FROM players LIMIT 1`, kept)
	if err != nil || tag.RowsAffected() != 1 {
		t.Fatalf("insert expired code %v rows %d", err, tag.RowsAffected())
	}
	if err := srv.store.CleanupOAuth(context.Background()); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM oauth_clients WHERE client_id=$1`, kept).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("unused client was kept")
	}
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM oauth_codes WHERE code_hash='expired-code'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("expired code was kept")
	}
}

func TestOAuthFamilyLifetimeCap(t *testing.T) {
	srv, ts := oauthServer(t)
	_, consent := oauthDevSession(t, ts, srv.store)
	redirectURI := "http://127.0.0.1/callback"
	client := registerOAuthClient(t, ts, map[string]any{
		"client_name": "Board", "redirect_uris": []string{redirectURI},
	})
	verifier, challenge, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	clientID := client["client_id"].(string)
	code := approveOAuth(t, ts, clientID, redirectURI, consent, verifier, challenge, "s", "")
	tokens := exchangeOAuthCode(t, ts, clientID, "", "", redirectURI, code, verifier)
	if _, err := testPool.Exec(context.Background(), `
		UPDATE oauth_refresh_tokens
		SET family_expires_at = now() - interval '1 minute', expires_at = now() + interval '1 day'
		WHERE token_hash=$1`, hashKey(tokens.RefreshToken)); err != nil {
		t.Fatal(err)
	}
	dead := tokenForm(t, ts, url.Values{
		"grant_type": {"refresh_token"}, "refresh_token": {tokens.RefreshToken}, "client_id": {clientID},
	})
	if dead.Error != "invalid_grant" {
		t.Fatalf("family cap %+v", dead)
	}
}

func TestBnetAttemptsPersistAndDoNotCollide(t *testing.T) {
	resetDB(t)
	store := NewStore(testPool)
	first := &Server{
		store: store, publicBaseURL: "http://127.0.0.1",
		bnetClientID: "cid", bnetClientSecret: "sec", bnet: newBnetFlow(),
	}
	ts := httptest.NewServer(first.Handler())
	t.Cleanup(ts.Close)
	first.publicBaseURL = ts.URL
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/auth/bnet/start?attempt=same", nil)
	if err != nil {
		t.Fatal(err)
	}
	client := ts.Client()
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || !strings.Contains(resp.Header.Get("Location"), "code_challenge_method=S256") {
		t.Fatalf("start %d %s", resp.StatusCode, resp.Header.Get("Location"))
	}
	if err := store.UpsertBnetAttempt(context.Background(), bnetAttemptRecord{
		Kind: bnetKindOAuth, ID: "same", Verifier: "verifier", State: "oauth-state", OAuthRequestID: "same",
	}); err != nil {
		t.Fatal(err)
	}
	if err := store.SetBnetAttemptSession(context.Background(), bnetKindOAuth, "same", sessionWire{SessionToken: "gss_should_not_leak"}); err != nil {
		t.Fatal(err)
	}
	second := httptest.NewServer((&Server{store: store, bnet: newBnetFlow()}).Handler())
	t.Cleanup(second.Close)
	poll, err := http.Get(second.URL + "/v1/auth/bnet/poll?attempt=same")
	if err != nil {
		t.Fatal(err)
	}
	defer poll.Body.Close()
	raw, _ := io.ReadAll(poll.Body)
	if poll.StatusCode != http.StatusAccepted || strings.Contains(string(raw), "gss_should_not_leak") {
		t.Fatalf("poll %d %s", poll.StatusCode, raw)
	}
}

func playerIDFromConsent(t *testing.T, srv *Server, consent string) string {
	t.Helper()
	p, err := srv.store.PlayerByConsentSession(context.Background(), consent)
	if err != nil {
		t.Fatal(err)
	}
	return p.ID
}

type oauthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	Resource     string `json:"resource"`
	Error        string `json:"error"`
}

func oauthServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	resetDB(t)
	srv := &Server{store: NewStore(testPool), devToken: "dev-secret", publicBaseURL: "http://127.0.0.1"}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	srv.publicBaseURL = ts.URL
	return srv, ts
}

func oauthDevSession(t *testing.T, ts *httptest.Server, store *Store) (apiToken, consent string) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/v1/auth/dev", strings.NewReader(`{"bnet_account_id":"oauth-acct","battletag":"OAuth#1000","display_name":"OAuth"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer dev-secret")
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var session contract.SessionResponse
	if err := json.NewDecoder(resp.Body).Decode(&session); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || !strings.HasPrefix(session.SessionToken, "gss_") || session.Player.ID == "" {
		t.Fatalf("session %+v %d", session, resp.StatusCode)
	}
	consent, err = store.IssueConsentSession(context.Background(), session.Player.ID)
	if err != nil || !strings.HasPrefix(consent, "goc_") {
		t.Fatalf("consent %s %v", consent, err)
	}
	return session.SessionToken, consent
}

func registerOAuthClient(t *testing.T, ts *httptest.Server, body map[string]any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("register %+v %d", out, resp.StatusCode)
	}
	return out
}

func pkceChallenge(t *testing.T) string {
	t.Helper()
	_, challenge, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	return challenge
}

func authorizeQuery(clientID, redirectURI, challenge, state, resource string) string {
	q := url.Values{}
	q.Set("client_id", clientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("response_type", "code")
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if resource != "" {
		q.Set("resource", resource)
	}
	return "/oauth/authorize?" + q.Encode()
}

func oauthGet(t *testing.T, ts *httptest.Server, path, session string, follow bool) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if session != "" {
		req.AddCookie(&http.Cookie{Name: oauthCookieName, Value: session})
	}
	client := ts.Client()
	if !follow {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func approveOAuth(t *testing.T, ts *httptest.Server, clientID, redirectURI, session, verifier, challenge, state, resource string) string {
	t.Helper()
	_ = verifier
	page := oauthGet(t, ts, authorizeQuery(clientID, redirectURI, challenge, state, resource), session, true)
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if page.StatusCode != http.StatusOK || !strings.Contains(string(body), "OAuth#1000") {
		t.Fatalf("consent page %d %s", page.StatusCode, body)
	}
	requestID := oauthRequestID(t, string(body))
	csrf := oauthCSRF(t, string(body))
	resp := postFormNoFollow(t, ts.URL+"/oauth/authorize", url.Values{
		"request": {requestID}, "decision": {"approve"}, "csrf": {csrf},
	}, session, ts.URL)
	loc := oauthDecisionTarget(t, ts, resp)
	if loc.Query().Get("code") == "" || loc.Query().Get("state") != state || loc.Query().Get("iss") == "" {
		t.Fatalf("approve %s", loc)
	}
	if !strings.HasPrefix(loc.Query().Get("code"), "gso_") {
		t.Fatalf("code %s", loc.Query().Get("code"))
	}
	return loc.Query().Get("code")
}

func oauthDecisionTarget(t *testing.T, ts *httptest.Server, resp *http.Response) *url.URL {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal(err)
	}
	htmlBody := string(body)
	switch resp.StatusCode {
	case http.StatusSeeOther:
		handoff, err := url.Parse(resp.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		issuer, err := url.Parse(ts.URL)
		if err != nil {
			t.Fatal(err)
		}
		if handoff.Scheme != issuer.Scheme || handoff.Host != issuer.Host || handoff.Path != "/oauth/continue" || handoff.Query().Get("ticket") == "" {
			t.Fatalf("handoff %s", resp.Header.Get("Location"))
		}
		if strings.Contains(resp.Header.Get("Location"), "code=") {
			t.Fatalf("code in handoff url %s", resp.Header.Get("Location"))
		}
		page, err := http.Get(resp.Header.Get("Location"))
		if err != nil {
			t.Fatal(err)
		}
		pageBody, err := io.ReadAll(page.Body)
		page.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if page.StatusCode != http.StatusOK {
			t.Fatalf("continue %d %s", page.StatusCode, pageBody)
		}
		htmlBody = string(pageBody)
	case http.StatusOK:
	default:
		t.Fatalf("decision %d %s %s", resp.StatusCode, resp.Header.Get("Location"), body)
	}
	if !strings.Contains(htmlBody, "Sending you back") {
		t.Fatalf("interstitial %s", htmlBody)
	}
	return oauthHandoffTarget(t, htmlBody)
}

func oauthHandoffTarget(t *testing.T, page string) *url.URL {
	t.Helper()
	m := oauthHandoffLink.FindStringSubmatch(page)
	if len(m) != 2 || !strings.Contains(page, `http-equiv="refresh"`) {
		t.Fatalf("handoff page %s", page)
	}
	target, err := url.Parse(html.UnescapeString(m[1]))
	if err != nil {
		t.Fatal(err)
	}
	return target
}

var oauthHandoffLink = regexp.MustCompile(`href="([^"]+)"`)

var oauthRequestPattern = regexp.MustCompile(`name="request" value="([^"]+)"`)
var oauthCSRFPattern = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func oauthRequestID(t *testing.T, html string) string {
	t.Helper()
	m := oauthRequestPattern.FindStringSubmatch(html)
	if len(m) != 2 {
		t.Fatalf("request id not in %s", html)
	}
	return m[1]
}

func oauthCSRF(t *testing.T, html string) string {
	t.Helper()
	m := oauthCSRFPattern.FindStringSubmatch(html)
	if len(m) != 2 || m[1] == "" {
		t.Fatalf("csrf not in %s", html)
	}
	return m[1]
}

func exchangeOAuthCode(t *testing.T, ts *httptest.Server, clientID, secret, method, redirectURI, code, verifier string) oauthTokenResponse {
	t.Helper()
	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {redirectURI}, "code_verifier": {verifier},
	}
	if method != "basic" {
		form.Set("client_id", clientID)
		if secret != "" {
			form.Set("client_secret", secret)
		}
	}
	req, err := http.NewRequest(http.MethodPost, ts.URL+"/oauth/token", strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if method == "basic" {
		req.SetBasicAuth(clientID, secret)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out oauthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || out.Error != "" || out.TokenType != "Bearer" {
		t.Fatalf("exchange %+v %d", out, resp.StatusCode)
	}
	return out
}

func tokenForm(t *testing.T, ts *httptest.Server, form url.Values) oauthTokenResponse {
	t.Helper()
	resp := postForm(t, ts.URL+"/oauth/token", form)
	defer resp.Body.Close()
	var out oauthTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	return out
}

func postForm(t *testing.T, rawURL string, form url.Values) *http.Response {
	t.Helper()
	resp, err := http.PostForm(rawURL, form)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func openOAuthConsent(t *testing.T, ts *httptest.Server, clientID, redirectURI, consent string) (policy, requestID, csrf string) {
	t.Helper()
	_, challenge, err := pkcePair()
	if err != nil {
		t.Fatal(err)
	}
	page := oauthGet(t, ts, authorizeQuery(clientID, redirectURI, challenge, "origin-state", ""), consent, true)
	defer page.Body.Close()
	html, _ := io.ReadAll(page.Body)
	if page.StatusCode != http.StatusOK {
		t.Fatalf("consent page %d %s", page.StatusCode, html)
	}
	return page.Header.Get("Referrer-Policy"), oauthRequestID(t, string(html)), oauthCSRF(t, string(html))
}

func postConsentDecision(t *testing.T, rawURL string, form url.Values, session, origin, secFetchSite string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if secFetchSite != "" {
		req.Header.Set("Sec-Fetch-Site", secFetchSite)
	}
	if session != "" {
		req.AddCookie(&http.Cookie{Name: oauthCookieName, Value: session})
	}
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func postFormNoFollow(t *testing.T, rawURL string, form url.Values, session, origin string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	if session != "" {
		req.AddCookie(&http.Cookie{Name: oauthCookieName, Value: session})
	}
	client := &http.Client{CheckRedirect: func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func oauthStatus(t *testing.T, ts *httptest.Server, token string) int {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/groups", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}
