package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/webhook"
)

var testPool *pgxpool.Pool

func TestMain(m *testing.M) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://sagalore:sagalore@127.0.0.1:5432/sagalore_test?sslmode=disable"
	}
	u, err := url.Parse(dsn)
	if err != nil || !strings.Contains(u.Path, "test") {
		fmt.Fprintln(os.Stderr, "refusing to run API tests against a database whose name does not contain \"test\"")
		os.Exit(1)
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := pool.Ping(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "postgres:", err)
		os.Exit(1)
	}
	if err := migrate(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, "migrate:", err)
		os.Exit(1)
	}
	if err := migrate(ctx, pool); err != nil {
		fmt.Fprintln(os.Stderr, "migrate twice:", err)
		os.Exit(1)
	}
	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

func resetDB(t *testing.T) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `
		TRUNCATE adventure_log, progress, screenshots, quest_versions, quest_assignments, quests, group_invites, group_members, characters, personal_tokens, sessions, groups, players, oauth_codes, oauth_refresh_tokens, oauth_requests, oauth_approve_replays, oauth_handoffs, oauth_clients, oauth_consent_sessions, bnet_attempts, oauth_register_attempts CASCADE`)
	if err != nil {
		t.Fatal(err)
	}
}

type webhookSink struct {
	mu     sync.Mutex
	events []webhook.Event
	server *httptest.Server
	url    string
	queue  *webhook.Queue
}

func newWebhookSink(t *testing.T) *webhookSink {
	t.Helper()
	s := &webhookSink{}
	s.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var ev webhook.Event
		if err := json.NewDecoder(r.Body).Decode(&ev); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		s.mu.Lock()
		s.events = append(s.events, ev)
		s.mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(s.server.Close)
	s.url = s.server.URL
	s.queue = webhook.NewQueue(&webhook.Client{HTTP: s.server.Client()})
	return s
}

func (s *webhookSink) snapshot() []webhook.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]webhook.Event(nil), s.events...)
}

func (s *webhookSink) waitEvents(t *testing.T, n int) []webhook.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		out := s.snapshot()
		if len(out) >= n {
			return out
		}
		time.Sleep(20 * time.Millisecond)
	}
	out := s.snapshot()
	t.Fatalf("want %d webhook events, got %d: %+v", n, len(out), out)
	return nil
}

func (s *webhookSink) waitType(t *testing.T, typ string) webhook.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		for _, ev := range s.snapshot() {
			if ev.Type == typ {
				return ev
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("missing webhook type %s in %+v", typ, s.snapshot())
	return webhook.Event{}
}

func (s *webhookSink) countType(typ string) int {
	n := 0
	for _, ev := range s.snapshot() {
		if ev.Type == typ {
			n++
		}
	}
	return n
}

type apiClient struct {
	base  string
	token string
	http  *http.Client
}

func (c apiClient) do(method, path string, body any, out any) (*http.Response, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, rdr)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return resp, fmt.Errorf("status %d decode %s: %w", resp.StatusCode, raw, err)
		}
	}
	resp.Body = io.NopCloser(bytes.NewReader(raw))
	return resp, nil
}

func TestHealthz(t *testing.T) {
	srv := httptest.NewServer((&Server{store: NewStore(testPool), adminToken: "admin"}).Handler())
	defer srv.Close()
	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestCompletionIdempotentAndQuestVersions(t *testing.T) {
	resetDB(t)
	sink := newWebhookSink(t)
	store := NewStore(testPool)
	srv := httptest.NewServer((&Server{store: store, adminToken: "admin-test", webhooks: sink.queue}).Handler())
	t.Cleanup(srv.Close)
	admin := apiClient{base: srv.URL, token: "admin-test", http: srv.Client()}

	magnusPlayer, magnusToken := devSession(t, "bnet-magnus", "Magnus#2231", "Magnus")
	otherPlayer, otherToken := devSession(t, "bnet-other", "Other#1", "Other")
	group := devGroup(t, magnusPlayer.ID, "tutorial", "Tutorial")
	if err := store.SetGroupWebhook(context.Background(), magnusPlayer.ID, group.ID, sink.url); err != nil {
		t.Fatal(err)
	}
	created := contract.CreatePlayerResponse{Player: magnusPlayer, APIKey: magnusToken}
	other := contract.CreatePlayerResponse{Player: otherPlayer, APIKey: otherToken}
	var resp *http.Response
	var err error
	var magni contract.Character
	resp, err = admin.do(http.MethodPost, "/v1/admin/characters", map[string]any{
		"player_id": created.Player.ID, "name": "Magni", "realm": "Argent Dawn",
		"race": "Dwarf", "class": "Paladin", "level": 12,
	}, &magni)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("character %d %v", resp.StatusCode, err)
	}
	var quest contract.Quest
	author := apiClient{base: srv.URL, token: magnusToken, http: srv.Client()}
	resp, err = author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Første vakt", "description": "Stå vakt.",
		"objectives": []map[string]any{
			{"id": "meet", "type": "interact", "text": "Møt opp", "npc": "Vakt"},
			{"id": "stay", "type": "location", "text": "Hold ut", "zone": "Elwynn Forest"},
		},
		"min_level": 10, "party": false,
		"assigned_character_ids": []string{magni.ID},
	}, &quest)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("quest %d %v", resp.StatusCode, err)
	}
	if quest.Version != 1 || quest.CreatedBy != "Magnus#2231" {
		t.Fatalf("quest %+v", quest)
	}

	player := apiClient{base: srv.URL, token: created.APIKey, http: srv.Client()}
	pngBytes := tinyPNG(t)
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	for k, v := range map[string]string{
		"character_id": magni.ID, "saga_id": quest.ID, "saga_version": "1",
		"battletag": "Magnus#2231", "level": "12", "completed_at": "2026-10-22T14:44:00Z",
	} {
		if err := mw.WriteField(k, v); err != nil {
			t.Fatal(err)
		}
	}
	fw, err := mw.CreateFormFile("screenshot", "shot.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(pngBytes); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/complete", bytes.NewReader(body.Bytes()))
	req.Header.Set("Authorization", "Bearer "+created.APIKey)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = player.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var first contract.CompletionResponse
	if err := json.NewDecoder(resp.Body).Decode(&first); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || !first.Completed || first.AlreadyCompleted || !first.LogWritten || !first.WebhookPosted {
		t.Fatalf("first %+v status %d", first, resp.StatusCode)
	}
	if first.QuestVersion != 1 || first.QuestTitle != "Første vakt" {
		t.Fatalf("recorded %+v", first)
	}
	completed := sink.waitType(t, webhook.TypeSagaCompleted)
	if completed.Saga == nil || completed.Saga.Title != "Første vakt" {
		t.Fatalf("webhook %+v", completed)
	}
	if completed.Character == nil || completed.Character.Name != "Magni" {
		t.Fatalf("character %+v", completed.Character)
	}
	if sink.countType(webhook.TypeSagaCompleted) != 1 {
		t.Fatalf("completed count %d", sink.countType(webhook.TypeSagaCompleted))
	}

	var second contract.CompletionResponse
	resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: quest.ID, QuestVersion: 1, BattleTag: "Magnus#2231", Level: 12,
	}, &second)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || !second.AlreadyCompleted || second.LogWritten || second.QuestVersion != 1 {
		t.Fatalf("second %+v status %d", second, resp.StatusCode)
	}
	time.Sleep(50 * time.Millisecond)
	if sink.countType(webhook.TypeSagaCompleted) != 1 {
		t.Fatalf("webhook posted again: %d", sink.countType(webhook.TypeSagaCompleted))
	}

	var updated contract.Quest
	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+quest.ID, map[string]any{
		"title": "Første vakt, igjen", "changes": []string{"Nytt navn"},
	}, &updated)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("patch %d %v", resp.StatusCode, err)
	}
	if updated.Version != 2 || updated.Title != "Første vakt, igjen" {
		t.Fatalf("updated %+v", updated)
	}
	var detail contract.QuestDetail
	resp, err = admin.do(http.MethodGet, "/v1/admin/sagas/"+quest.ID, nil, &detail)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err, resp.StatusCode)
	}
	if len(detail.Versions) != 2 || detail.Versions[0].Title != "Første vakt" || detail.Versions[0].Version != 1 {
		t.Fatalf("snapshots %+v", detail.Versions)
	}
	if detail.Versions[1].Version != 2 || detail.Versions[1].Title != "Første vakt, igjen" {
		t.Fatalf("new snapshot %+v", detail.Versions[1])
	}

	var third contract.CompletionResponse
	resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: quest.ID, QuestVersion: 2, BattleTag: "Magnus#2231",
	}, &third)
	if err != nil {
		t.Fatal(err)
	}
	if !third.AlreadyCompleted || third.QuestVersion != 1 {
		t.Fatalf("version bump must not open a second completion: %+v", third)
	}
	time.Sleep(50 * time.Millisecond)
	if sink.countType(webhook.TypeSagaCompleted) != 1 {
		t.Fatalf("webhook completed count %d", sink.countType(webhook.TypeSagaCompleted))
	}

	resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: quest.ID, QuestVersion: 99, BattleTag: "Magnus#2231",
	}, &contract.ErrorBody{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("unknown version status %d", resp.StatusCode)
	}

	var syncOut contract.SyncResponse
	resp, err = player.do(http.MethodGet, "/v1/sync", nil, &syncOut)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err, resp.StatusCode)
	}
	if syncOut.BattleTag != "Magnus#2231" || len(syncOut.Characters) != 1 || len(syncOut.Progress) != 1 {
		t.Fatalf("sync %+v", syncOut)
	}
	var syncedQuest *contract.Quest
	for i := range syncOut.Quests {
		if syncOut.Quests[i].ID == quest.ID {
			syncedQuest = &syncOut.Quests[i]
			break
		}
	}
	if syncedQuest == nil {
		t.Fatalf("sync missing quest %s in %+v", quest.ID, syncOut.Quests)
	}
	if syncedQuest.Version != 2 || syncOut.Progress[0].QuestVersion != 1 || syncOut.Progress[0].Status != "completed" {
		t.Fatalf("sync versions quest=%d progress=%+v", syncedQuest.Version, syncOut.Progress[0])
	}
	var shotCount int
	if err := testPool.QueryRow(context.Background(), `SELECT count(*) FROM screenshots`).Scan(&shotCount); err != nil || shotCount != 1 {
		t.Fatalf("screenshot rows %d %v", shotCount, err)
	}
	if len(syncOut.Completions) != 1 || syncOut.Completions[0].Character != "Magni" || syncOut.Completions[0].QuestID != quest.ID {
		t.Fatalf("completions %+v", syncOut.Completions)
	}

	var logs struct {
		Entries []contract.LogEntry `json:"entries"`
	}
	resp, err = admin.do(http.MethodGet, "/v1/admin/log?battletag=Magnus%232231", nil, &logs)
	if err != nil || resp.StatusCode != 200 {
		t.Fatal(err, resp.StatusCode)
	}
	if len(logs.Entries) != 1 || !strings.Contains(logs.Entries[0].Body, "Magni completed") {
		t.Fatalf("log %+v", logs.Entries)
	}

	var listed struct {
		Quests []contract.QuestListItem `json:"sagas"`
	}
	resp, err = admin.do(http.MethodGet, "/v1/admin/sagas?character_id="+magni.ID, nil, &listed)
	if err != nil {
		t.Fatal(err)
	}
	var listedQuest *contract.QuestListItem
	for i := range listed.Quests {
		if listed.Quests[i].ID == quest.ID {
			listedQuest = &listed.Quests[i]
			break
		}
	}
	if listedQuest == nil || listedQuest.Status == nil || *listedQuest.Status != "completed" || listedQuest.Eligible == nil || !*listedQuest.Eligible {
		t.Fatalf("list %+v", listed.Quests)
	}

	var lvl contract.LevelResponse
	resp, err = player.do(http.MethodPost, "/v1/level", contract.LevelRequest{
		CharacterID: magni.ID, BattleTag: "Magnus#2231", Level: 13,
	}, &lvl)
	if err != nil || !lvl.Changed || lvl.Level != 13 {
		t.Fatalf("level %+v %v status %d", lvl, err, statusOf(resp))
	}
	resp, err = player.do(http.MethodPost, "/v1/level", contract.LevelRequest{
		CharacterID: magni.ID, BattleTag: "Magnus#2231", Level: 13,
	}, &lvl)
	if err != nil || lvl.Changed {
		t.Fatalf("level repeat %+v", lvl)
	}

	resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: quest.ID, QuestVersion: 1, BattleTag: "Other#1",
	}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("mismatched tag %d", statusOf(resp))
	}
	otherClient := apiClient{base: srv.URL, token: other.APIKey, http: srv.Client()}
	resp, err = otherClient.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: quest.ID, QuestVersion: 1, BattleTag: "Other#1",
	}, nil)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("other player %d", statusOf(resp))
	}
	resp, err = http.Get(srv.URL + "/v1/sync")
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no key %d", statusOf(resp))
	}
}

func devSession(t *testing.T, account, tag, name string) (contract.Player, string) {
	t.Helper()
	p, err := NewStore(testPool).MergeBattleNet(context.Background(), account, tag, name)
	if err != nil {
		t.Fatal(err)
	}
	sess, err := NewStore(testPool).IssueSessionFor(context.Background(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	return sess.Player, sess.SessionToken
}

func joinGroup(t *testing.T, groupID, playerID string) {
	t.Helper()
	_, err := testPool.Exec(context.Background(), `
		INSERT INTO group_members (group_id, player_id, role) VALUES ($1,$2,'member')
		ON CONFLICT DO NOTHING`, groupID, playerID)
	if err != nil {
		t.Fatal(err)
	}
}

func devGroup(t *testing.T, ownerID, slug, name string) contract.GroupSummary {
	t.Helper()
	g, err := NewStore(testPool).CreateGroup(context.Background(), ownerID, slug, name, "en")
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestGroupSagaCreditsEachCharacter(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool), adminToken: "admin-test"}).Handler())
	t.Cleanup(srv.Close)
	admin := apiClient{base: srv.URL, token: "admin-test", http: srv.Client()}

	magnusPlayer, magnusToken := devSession(t, "bnet-magnus", "Magnus#2231", "Magnus")
	otherPlayer, otherToken := devSession(t, "bnet-mira", "Mira#1", "Mira")
	group := devGroup(t, magnusPlayer.ID, "tutorial", "Tutorial")
	joinGroup(t, group.ID, otherPlayer.ID)
	magnus := contract.CreatePlayerResponse{Player: magnusPlayer, APIKey: magnusToken}
	other := contract.CreatePlayerResponse{Player: otherPlayer, APIKey: otherToken}
	author := apiClient{base: srv.URL, token: magnusToken, http: srv.Client()}
	var resp *http.Response
	var err error
	var magni, mira contract.Character
	resp, err = admin.do(http.MethodPost, "/v1/admin/characters", map[string]any{
		"player_id": magnus.Player.ID, "name": "Magni", "realm": "Argent Dawn",
		"race": "Dwarf", "class": "Paladin", "level": 18,
	}, &magni)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatal(err)
	}
	resp, err = admin.do(http.MethodPost, "/v1/admin/characters", map[string]any{
		"player_id": other.Player.ID, "name": "Mira", "realm": "Argent Dawn",
		"race": "Human", "class": "Mage", "level": 17,
	}, &mira)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatal(err)
	}

	var bad contract.ErrorBody
	resp, err = author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Ugyldig emote", "party": true,
		"objectives": []map[string]any{{"id": "hi", "type": "emote", "text": "Hils"}},
	}, &bad)
	if err != nil || resp.StatusCode != http.StatusBadRequest || !strings.Contains(bad.Message, "emote") {
		t.Fatalf("bad emote %d %+v %v", statusOf(resp), bad, err)
	}

	level := 17
	var quest contract.Quest
	resp, err = author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Deadmines", "description": "Fire stykker.",
		"party": true, "min_level": 17,
		"when":  map[string]any{"clock": "game", "start": "18:00", "end": "06:00"},
		"group": map[string]any{"size": 2, "min_level": level, "instance": "The Deadmines"},
		"objectives": []map[string]any{
			{"id": "door", "type": "emote", "text": "Hils på Rhahk'Zor", "emote": "/salute", "npc": "Rhahk'Zor"},
			{"id": "both", "type": "all_of", "text": "Inne i gruven med vann", "when": map[string]any{"start": "22:00", "end": "02:00"}, "objectives": []map[string]any{
				{"id": "mine", "type": "location", "text": "Deadmines", "zone": "The Deadmines"},
				{"id": "water", "type": "collect", "text": "Vann", "item_id": 159},
			}},
		},
	}, &quest)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("quest %d %v %+v", statusOf(resp), err, quest)
	}
	if quest.Group == nil || quest.Group.Size != 2 || quest.When == nil || quest.When.Clock != "game" {
		t.Fatalf("rules %+v when %+v", quest.Group, quest.When)
	}
	if quest.Objectives[0].Emote != "SALUTE" || len(quest.Objectives[1].Children) != 2 || quest.Objectives[1].Children[1].Count != 1 {
		t.Fatalf("objectives %+v", quest.Objectives)
	}

	player := apiClient{base: srv.URL, token: magnus.APIKey, http: srv.Client()}
	var denied contract.ErrorBody
	resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: quest.ID, QuestVersion: 1, BattleTag: "Magnus#2231", Level: 18,
		Instance:     "The Deadmines",
		GroupMembers: []contract.GroupMember{{Name: "Magni", Realm: "Argent Dawn", Level: 18}},
	}, &denied)
	if err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("solo group %d %+v %v", statusOf(resp), denied, err)
	}

	var first contract.CompletionResponse
	resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: quest.ID, QuestVersion: 1, BattleTag: "Magnus#2231", Level: 18,
		Instance: "Deadmines",
		GroupMembers: []contract.GroupMember{
			{Name: "Magni", Realm: "Argent Dawn", Level: 18},
			{Name: "Mira", Realm: "Argent Dawn", Level: 17},
		},
	}, &first)
	if err != nil || resp.StatusCode != http.StatusOK || !first.Completed || first.AlreadyCompleted {
		t.Fatalf("complete %+v %d %v", first, statusOf(resp), err)
	}
	if len(first.GroupCredited) != 1 || first.GroupCredited[0].CharacterID != mira.ID || first.GroupCredited[0].Already {
		t.Fatalf("credit %+v", first.GroupCredited)
	}

	var second contract.CompletionResponse
	resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: quest.ID, QuestVersion: 1, BattleTag: "Magnus#2231", Level: 18,
		Instance: "The Deadmines",
		GroupMembers: []contract.GroupMember{
			{Name: "Mira", Realm: "Argent Dawn", Level: 17},
		},
	}, &second)
	if err != nil || !second.AlreadyCompleted || len(second.GroupCredited) != 1 || !second.GroupCredited[0].Already {
		t.Fatalf("repeat %+v %v", second, err)
	}

	miraClient := apiClient{base: srv.URL, token: other.APIKey, http: srv.Client()}
	var syncOut contract.SyncResponse
	resp, err = miraClient.do(http.MethodGet, "/v1/sync", nil, &syncOut)
	if err != nil || resp.StatusCode != 200 || len(syncOut.Progress) != 1 || syncOut.Progress[0].Status != "completed" || syncOut.Progress[0].CharacterID != mira.ID {
		t.Fatalf("mira sync %+v %d %v", syncOut.Progress, statusOf(resp), err)
	}
}

func TestDevLoginUpsertsCharacter(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool), devToken: "dev-secret"}).Handler())
	t.Cleanup(srv.Close)
	client := apiClient{base: srv.URL, token: "nope", http: srv.Client()}
	resp, err := client.do(http.MethodPost, "/v1/auth/dev", nil, nil)
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("bad token %d", statusOf(resp))
	}
	var session contract.SessionResponse
	client.token = "dev-secret"
	resp, err = client.do(http.MethodPost, "/v1/auth/dev", map[string]string{
		"bnet_account_id": "bnet-magnus", "battletag": "Magnus#2231", "display_name": "Magnus",
	}, &session)
	if err != nil || resp.StatusCode != http.StatusOK || !strings.HasPrefix(session.SessionToken, "gss_") {
		t.Fatalf("session %+v %d %v", session, statusOf(resp), err)
	}
	if session.Player.BattleTag != "Magnus#2231" || session.Player.BnetAccountID != "bnet-magnus" {
		t.Fatalf("identity %+v", session.Player)
	}
	player := apiClient{base: srv.URL, token: session.SessionToken, http: srv.Client()}
	var ch contract.Character
	resp, err = player.do(http.MethodPut, "/v1/characters", contract.CharacterUpsert{
		Name: "Magni", Realm: "Stormwind", Level: 12, Race: "Dwarf", Class: "Warrior", BattleTag: "Magnus#2231",
	}, &ch)
	if err != nil || resp.StatusCode != http.StatusOK || ch.Name != "Magni" || ch.BattleTag != "Magnus#2231" {
		t.Fatalf("upsert %+v %d %v", ch, statusOf(resp), err)
	}
	resp, err = player.do(http.MethodPut, "/v1/characters", contract.CharacterUpsert{
		Name: "Magni", Realm: "Stormwind", Level: 13, Race: "Dwarf", Class: "Warrior", BattleTag: "Magnus#2231",
	}, &ch)
	if err != nil || ch.Level != 13 || ch.ID == "" {
		t.Fatalf("second %+v %v", ch, err)
	}
	var sync contract.SyncResponse
	resp, err = player.do(http.MethodGet, "/v1/sync", nil, &sync)
	if err != nil || resp.StatusCode != 200 || len(sync.Characters) != 1 || sync.BattleTag != "Magnus#2231" {
		t.Fatalf("sync %+v %d", sync, statusOf(resp))
	}
}

func TestAdminQuestCreateWithOptionalImage(t *testing.T) {
	resetDB(t)
	srv := httptest.NewServer((&Server{store: NewStore(testPool), adminToken: "admin-img"}).Handler())
	t.Cleanup(srv.Close)
	admin := apiClient{base: srv.URL, token: "admin-img", http: srv.Client()}

	playerRec, token := devSession(t, "bnet-magnus", "Magnus#2231", "Magnus")
	devGroup(t, playerRec.ID, "tutorial", "Tutorial")
	author := apiClient{base: srv.URL, token: token, http: srv.Client()}

	var noImg contract.Quest
	resp, err := author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Uten bilde", "description": "Bare tekst.",
		"objectives": []map[string]any{
			{"id": "talk", "type": "interact", "text": "Snakk", "npc": "Tharynn Bouden"},
		},
		"party": true,
	}, &noImg)
	if err != nil || resp.StatusCode != http.StatusCreated || noImg.HasImage {
		t.Fatalf("json create %+v %d %v", noImg, statusOf(resp), err)
	}

	pngBytes := tinyPNG(t)
	body := &bytes.Buffer{}
	mw := multipart.NewWriter(body)
	_ = mw.WriteField("title", "Med bilde")
	_ = mw.WriteField("description", "Cover.")
	_ = mw.WriteField("objectives", `[{"id":"talk","type":"interact","text":"Snakk","npc":"Tharynn Bouden"}]`)
	_ = mw.WriteField("party", "true")
	_ = mw.WriteField("party", "true")
	fw, err := mw.CreateFormFile("image", "cover.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(pngBytes); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/v1/sagas", bytes.NewReader(body.Bytes()))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err = admin.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	var withImg contract.Quest
	if err := json.Unmarshal(raw, &withImg); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if resp.StatusCode != http.StatusCreated || !withImg.HasImage || withImg.ImageURL == "" {
		t.Fatalf("multipart create %+v status %d body %s", withImg, resp.StatusCode, raw)
	}

	patchBody := &bytes.Buffer{}
	pmw := multipart.NewWriter(patchBody)
	fw, err = pmw.CreateFormFile("image", "cover2.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Write(pngBytes); err != nil {
		t.Fatal(err)
	}
	if err := pmw.Close(); err != nil {
		t.Fatal(err)
	}
	req, _ = http.NewRequest(http.MethodPatch, srv.URL+"/v1/sagas/"+noImg.ID, bytes.NewReader(patchBody.Bytes()))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", pmw.FormDataContentType())
	resp, err = admin.http.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	var patched contract.Quest
	if err := json.Unmarshal(raw, &patched); err != nil {
		t.Fatalf("patch decode %s: %v", raw, err)
	}
	if resp.StatusCode != 200 || !patched.HasImage || patched.Version != 1 {
		t.Fatalf("cover-only patch should not bump version: %+v status %d", patched, resp.StatusCode)
	}
}

func TestSeedQuests(t *testing.T) {
	resetDB(t)
	store := NewStore(testPool)
	created, updated, err := seed(context.Background(), store)
	if err != nil || created != 3 || updated != 0 {
		t.Fatalf("created %d updated %d err %v", created, updated, err)
	}
	created, updated, err = seed(context.Background(), store)
	if err != nil || created != 0 || updated != 3 {
		t.Fatalf("second created %d updated %d err %v", created, updated, err)
	}
	list, err := store.ListQuests(context.Background(), "")
	if err != nil || len(list) != 3 {
		t.Fatalf("list %d %v", len(list), err)
	}
	for _, q := range list {
		if !q.PartyQuest || len(q.Objectives) == 0 {
			t.Fatalf("%s objectives %+v", q.Title, q.Objectives)
		}
	}
}

func statusOf(resp *http.Response) int {
	if resp == nil {
		return 0
	}
	return resp.StatusCode
}

func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 1, 1))
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
