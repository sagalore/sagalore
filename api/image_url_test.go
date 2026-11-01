package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sagalore/sagalore/internal/contract"
)

func TestAdminAndSignedSagaImage(t *testing.T) {
	resetDB(t)
	const secret = "test-image-secret"
	srv := httptest.NewServer((&Server{
		store:          NewStore(testPool),
		adminToken:     "admin-test",
		publicBaseURL:  "https://sagalore.example.com",
		imageURLSecret: secret,
		imageURLTTL:    time.Hour,
	}).Handler())
	t.Cleanup(srv.Close)
	admin := apiClient{base: srv.URL, token: "admin-test", http: srv.Client()}

	playerRec, token := devSession(t, "bnet-magnus", "Magnus#2231", "Magnus")
	devGroup(t, playerRec.ID, "tutorial", "Tutorial")
	created := contract.CreatePlayerResponse{Player: playerRec, APIKey: token}
	player := apiClient{base: srv.URL, token: token, http: srv.Client()}

	var quest contract.Quest
	resp, err := player.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Forside", "description": "Et bilde.",
		"objectives": []map[string]any{
			{"id": "stay", "type": "location", "text": "Stå i Goldshire", "zone": "Elwynn Forest"},
		},
		"party": true,
	}, &quest)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("quest %d %v", statusOf(resp), err)
	}
	if quest.ImageSignedURL != "" || strings.Contains(mustJSON(t, quest), "image_signed_url") {
		t.Fatalf("unsigned create leaked a signed url: %+v", quest)
	}

	status, hdr, body := rawGet(t, srv.Client(), srv.URL+"/v1/admin/sagas/"+quest.ID+"/image", "admin-test")
	if status != http.StatusNotFound {
		t.Fatalf("admin missing image status %d body %s", status, body)
	}
	status, _, body = rawGet(t, srv.Client(), srv.URL+"/v1/admin/sagas/"+quest.ID+"/image", "")
	if status != http.StatusUnauthorized {
		t.Fatalf("admin image without token %d %s", status, body)
	}
	_ = hdr

	exp := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	status, _, body = rawGet(t, srv.Client(), publicImageURL(srv.URL, quest.ID, exp, signImage(secret, quest.ID, exp)), "")
	if status != http.StatusNotFound || strings.Contains(string(body), "Forside") {
		t.Fatalf("public no-image %d %s", status, body)
	}
	status, _, body = rawGet(t, srv.Client(), publicImageURL(srv.URL, quest.ID, exp, "00"), "")
	if status != http.StatusForbidden || !strings.Contains(string(body), "bad signature") || strings.Contains(string(body), "Forside") {
		t.Fatalf("public bad sig before image %d %s", status, body)
	}

	pngBytes := tinyPNG(t)
	if _, err := testPool.Exec(context.Background(),
		`UPDATE quests SET image_bytes=$1, image_content_type='image/png' WHERE id=$2`, pngBytes, quest.ID); err != nil {
		t.Fatal(err)
	}

	// Player endpoints (used by OAuth clients) must also carry the signed URL.
	var playerDetail contract.QuestDetail
	if resp, err := player.do(http.MethodGet, "/v1/sagas/"+quest.ID, nil, &playerDetail); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("player get saga %d %v", statusOf(resp), err)
	}
	if !strings.HasPrefix(playerDetail.Quest.ImageSignedURL, "https://sagalore.example.com/v1/public/sagas/"+quest.ID+"/image?exp=") {
		t.Fatalf("player get saga missing signed url: %q", playerDetail.Quest.ImageSignedURL)
	}
	var playerList struct {
		Sagas []contract.Quest `json:"sagas"`
	}
	if resp, err := player.do(http.MethodGet, "/v1/sagas", nil, &playerList); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("player list sagas %d %v", statusOf(resp), err)
	}
	if len(playerList.Sagas) != 1 || playerList.Sagas[0].ImageSignedURL == "" {
		t.Fatalf("player list sagas missing signed url: %+v", playerList.Sagas)
	}

	status, adminHdr, adminBody := rawGet(t, srv.Client(), srv.URL+"/v1/admin/sagas/"+quest.ID+"/image", "admin-test")
	if status != http.StatusOK || adminHdr.Get("Content-Type") != "image/png" || adminHdr.Get("Cache-Control") != "private, max-age=300" {
		t.Fatalf("admin image %d type %q cache %q", status, adminHdr.Get("Content-Type"), adminHdr.Get("Cache-Control"))
	}
	status, playerHdr, playerBody := rawGet(t, srv.Client(), srv.URL+"/v1/sagas/"+quest.ID+"/image", created.APIKey)
	if status != http.StatusOK || playerHdr.Get("Content-Type") != adminHdr.Get("Content-Type") || playerHdr.Get("Cache-Control") != adminHdr.Get("Cache-Control") {
		t.Fatalf("player image %d type %q cache %q", status, playerHdr.Get("Content-Type"), playerHdr.Get("Cache-Control"))
	}
	if string(adminBody) != string(pngBytes) || string(playerBody) != string(adminBody) {
		t.Fatalf("image bytes admin %d player %d png %d", len(adminBody), len(playerBody), len(pngBytes))
	}
	if playerHdr.Get("X-Content-Type-Options") != "" {
		t.Fatalf("player route gained nosniff: %q", playerHdr.Get("X-Content-Type-Options"))
	}

	var detail contract.QuestDetail
	resp, err = admin.do(http.MethodGet, "/v1/admin/sagas/"+quest.ID, nil, &detail)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("get quest %d %v", statusOf(resp), err)
	}
	if detail.ImageURL != "/v1/sagas/"+quest.ID+"/image" {
		t.Fatalf("image_url %q", detail.ImageURL)
	}
	if !strings.HasPrefix(detail.ImageSignedURL, "https://sagalore.example.com/v1/public/sagas/"+quest.ID+"/image?") {
		t.Fatalf("signed url %q", detail.ImageSignedURL)
	}
	if !strings.Contains(detail.ImageSignedURL, "exp=") || !strings.Contains(detail.ImageSignedURL, "sig=") {
		t.Fatalf("signed url missing query %q", detail.ImageSignedURL)
	}

	var listed struct {
		Quests []contract.QuestListItem `json:"sagas"`
	}
	resp, err = admin.do(http.MethodGet, "/v1/admin/sagas", nil, &listed)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("list %d %v", statusOf(resp), err)
	}
	var found bool
	for _, q := range listed.Quests {
		if q.ID == quest.ID {
			found = true
			if q.ImageSignedURL != detail.ImageSignedURL && !strings.HasPrefix(q.ImageSignedURL, "https://sagalore.example.com/v1/public/sagas/"+quest.ID+"/image?") {
				t.Fatalf("list signed url %q", q.ImageSignedURL)
			}
		}
	}
	if !found {
		t.Fatal("quest missing from admin list")
	}

	signedPath := strings.TrimPrefix(detail.ImageSignedURL, "https://sagalore.example.com")
	status, pubHdr, pubBody := rawGet(t, srv.Client(), srv.URL+signedPath, "")
	if status != http.StatusOK || pubHdr.Get("Content-Type") != "image/png" || pubHdr.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("public image %d type %q nosniff %q body %s", status, pubHdr.Get("Content-Type"), pubHdr.Get("X-Content-Type-Options"), pubBody)
	}
	if !strings.HasPrefix(pubHdr.Get("Cache-Control"), "public, max-age=") {
		t.Fatalf("public cache %q", pubHdr.Get("Cache-Control"))
	}
	if string(pubBody) != string(pngBytes) || strings.Contains(string(pubBody), "Forside") {
		t.Fatalf("public body len %d", len(pubBody))
	}

	last := signedPath[len(signedPath)-1]
	flip := byte('a')
	if last == 'a' {
		flip = 'b'
	}
	bad := signedPath[:len(signedPath)-1] + string(flip)
	status, _, body = rawGet(t, srv.Client(), srv.URL+bad, "")
	if status != http.StatusForbidden || !strings.Contains(string(body), "bad signature") || strings.Contains(string(body), quest.Title) {
		t.Fatalf("bad signature %d %s", status, body)
	}

	past := strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)
	status, _, body = rawGet(t, srv.Client(), publicImageURL(srv.URL, quest.ID, past, signImage(secret, quest.ID, past)), "")
	if status != http.StatusForbidden || !strings.Contains(string(body), "expired") || strings.Contains(string(body), quest.Title) {
		t.Fatalf("expired %d %s", status, body)
	}

	other := "11111111-1111-4111-8111-111111111111"
	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	status, _, body = rawGet(t, srv.Client(), publicImageURL(srv.URL, other, future, signImage(secret, other, future)), "")
	if status != http.StatusNotFound || strings.Contains(string(body), quest.Title) {
		t.Fatalf("unknown id valid sig %d %s", status, body)
	}
	status, _, body = rawGet(t, srv.Client(), publicImageURL(srv.URL, other, future, "deadbeef"), "")
	if status != http.StatusForbidden || strings.Contains(string(body), "not found") {
		t.Fatalf("unknown id bad sig should not reveal absence %d %s", status, body)
	}

	var saga contract.Quest
	resp, err = player.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Min saga", "description": "Uten signert url.",
		"objectives": []map[string]any{
			{"id": "stay", "type": "location", "text": "Stå", "zone": "Elwynn Forest"},
		},
		"party": true,
	}, &saga)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("saga %d %v", statusOf(resp), err)
	}
	if _, err := testPool.Exec(context.Background(),
		`UPDATE quests SET image_bytes=$1, image_content_type='image/png' WHERE id=$2`, pngBytes, saga.ID); err != nil {
		t.Fatal(err)
	}
	resp, err = player.do(http.MethodGet, "/v1/sagas/"+saga.ID, nil, &saga)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("get saga %d %v", statusOf(resp), err)
	}
	// Player (session or OAuth) clients may not be able to send a bearer
	// token when fetching the cover, so they get the short-lived signed URL
	// for a saga they can already read.
	if !strings.HasPrefix(saga.ImageSignedURL, "https://sagalore.example.com/v1/public/sagas/"+saga.ID+"/image?exp=") {
		t.Fatalf("player response missing signed url: %q", saga.ImageSignedURL)
	}
	if saga.ImageURL != "/v1/sagas/"+saga.ID+"/image" {
		t.Fatalf("player image_url %q", saga.ImageURL)
	}

	plain := httptest.NewServer((&Server{
		store:      NewStore(testPool),
		adminToken: "admin-test",
	}).Handler())
	t.Cleanup(plain.Close)
	plainAdmin := apiClient{base: plain.URL, token: "admin-test", http: plain.Client()}
	var plainDetail contract.QuestDetail
	resp, err = plainAdmin.do(http.MethodGet, "/v1/admin/sagas/"+quest.ID, nil, &plainDetail)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("unsigned admin get %d %v", statusOf(resp), err)
	}
	raw, _ := io.ReadAll(resp.Body)
	if plainDetail.ImageSignedURL != "" || strings.Contains(string(raw), "image_signed_url") {
		t.Fatalf("missing secret still signed: %s", raw)
	}
	status, _, body = rawGet(t, plain.Client(), publicImageURL(plain.URL, quest.ID, future, signImage(secret, quest.ID, future)), "")
	if status != http.StatusNotFound {
		t.Fatalf("public route without secret %d %s", status, body)
	}
}

func publicImageURL(base, id, exp, sig string) string {
	return base + "/v1/public/sagas/" + id + "/image?exp=" + exp + "&sig=" + sig
}

func rawGet(t *testing.T, client *http.Client, url, token string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header.Clone(), b
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
