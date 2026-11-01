package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/realtime"
)

func TestRealtimeTwoInstances(t *testing.T) {
	resetDB(t)
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		dsn = "postgres://sagalore:sagalore@127.0.0.1:5432/sagalore_test?sslmode=disable"
	}
	hub1 := realtime.NewHub()
	hub2 := realtime.NewHub()
	hub1.SetPingInterval(time.Hour)
	hub2.SetPingInterval(time.Hour)
	t.Cleanup(hub1.Close)
	t.Cleanup(hub2.Close)
	waitListen(t, hub1, dsn)
	waitListen(t, hub2, dsn)

	srv1 := httptest.NewServer((&Server{store: NewStore(testPool), adminToken: "admin-test", events: hub1}).Handler())
	srv2 := httptest.NewServer((&Server{store: NewStore(testPool), adminToken: "admin-test", events: hub2}).Handler())
	t.Cleanup(srv1.Close)
	t.Cleanup(srv2.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv1.URL, "http")+"/v1/ws", nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("missing bearer: status %d err %v", status, err)
	}

	admin := apiClient{base: srv1.URL, token: "admin-test", http: srv1.Client()}
	magnusPlayer, magnusToken := devSession(t, "bnet-magnus", "Magnus#2231", "Magnus")
	otherPlayer, otherToken := devSession(t, "bnet-other", "Other#1", "Other")
	group := devGroup(t, magnusPlayer.ID, "tutorial", "Tutorial")
	joinGroup(t, group.ID, otherPlayer.ID)
	magnus := contract.CreatePlayerResponse{Player: magnusPlayer, APIKey: magnusToken}
	other := contract.CreatePlayerResponse{Player: otherPlayer, APIKey: otherToken}
	author := apiClient{base: srv1.URL, token: magnusToken, http: srv1.Client()}
	var magni contract.Character
	resp, err = admin.do(http.MethodPost, "/v1/admin/characters", map[string]any{
		"player_id": magnus.Player.ID, "name": "Magni", "realm": "Argent Dawn",
		"race": "Dwarf", "class": "Paladin", "level": 12,
	}, &magni)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("character %d %v", statusOf(resp), err)
	}

	a := dialPlayer(t, srv1.URL, magnus.APIKey)
	b := dialPlayer(t, srv2.URL, other.APIKey)
	time.Sleep(50 * time.Millisecond)

	var quest contract.Quest
	resp, err = author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Første vakt", "description": "Stå vakt.",
		"objectives":             []map[string]any{{"id": "stay", "type": "location", "text": "Hold ut", "zone": "Elwynn Forest"}},
		"party":                  true,
		"assigned_character_ids": []string{magni.ID},
	}, &quest)
	if err != nil || resp.StatusCode != http.StatusCreated || quest.Version != 1 {
		t.Fatalf("quest %+v %d %v", quest, statusOf(resp), err)
	}
	wantBoth(t, a, b, realtime.TypeCreated, quest.ID, 1)

	player := apiClient{base: srv1.URL, token: magnus.APIKey, http: srv1.Client()}
	var done contract.CompletionResponse
	resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
		CharacterID: magni.ID, QuestID: quest.ID, QuestVersion: 1, BattleTag: "Magnus#2231", Level: 12,
	}, &done)
	if err != nil || resp.StatusCode != http.StatusOK || !done.Completed || done.AlreadyCompleted {
		t.Fatalf("complete %+v %d %v", done, statusOf(resp), err)
	}
	wantBoth(t, a, b, realtime.TypeCompleted, quest.ID, 1)
	withNoNotify(t, dsn, func() {
		resp, err = player.do(http.MethodPost, "/v1/complete", contract.CompletionRequest{
			CharacterID: magni.ID, QuestID: quest.ID, QuestVersion: 1, BattleTag: "Magnus#2231",
		}, &done)
		if err != nil || !done.AlreadyCompleted {
			t.Fatalf("second %+v %v", done, err)
		}
	})

	var updated contract.Quest
	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+quest.ID, map[string]any{
		"title": "Første vakt, igjen", "changes": []string{"Nytt navn"},
	}, &updated)
	if err != nil || updated.Version != 2 {
		t.Fatalf("patch %+v %v", updated, err)
	}
	wantBoth(t, a, b, realtime.TypeUpdated, quest.ID, 2)

	var retired contract.Quest
	resp, err = author.do(http.MethodPatch, "/v1/sagas/"+quest.ID, map[string]any{
		"active": false, "changes": []string{"Lagt bort"},
	}, &retired)
	if err != nil || retired.Version != 3 || retired.Active {
		t.Fatalf("retire %+v %v", retired, err)
	}
	wantBoth(t, a, b, realtime.TypeRetired, quest.ID, 3)

	withNoNotify(t, dsn, func() {
		if _, err := testPool.Exec(context.Background(), `UPDATE quests SET image_bytes=$2, image_content_type='image/png', updated_at=now() WHERE id=$1`, quest.ID, []byte{1, 2, 3}); err != nil {
			t.Fatal(err)
		}
	})

	var level contract.LevelResponse
	withNoNotify(t, dsn, func() {
		resp, err = player.do(http.MethodPost, "/v1/level", contract.LevelRequest{
			CharacterID: magni.ID, BattleTag: "Magnus#2231", Level: 12,
		}, &level)
		if err != nil || !level.Allowed || level.Changed {
			t.Fatalf("same level %+v %v", level, err)
		}
	})

	resp, err = player.do(http.MethodPost, "/v1/level", contract.LevelRequest{
		CharacterID: magni.ID, BattleTag: "Magnus#2231", Level: 13,
	}, &level)
	if err != nil || !level.Changed || level.Level != 13 {
		t.Fatalf("level %+v %v", level, err)
	}
	ev := readEvent(t, a)
	if ev.Type != realtime.TypeLevel || ev.PlayerID != magnus.Player.ID || ev.SagaID != "" || ev.Version != 0 {
		t.Fatalf("level event %+v player %s", ev, magnus.Player.ID)
	}
	expectQuiet(t, b)
}

func waitListen(t *testing.T, hub *realtime.Hub, dsn string) {
	t.Helper()
	ready := make(chan struct{})
	var once sync.Once
	go hub.ListenNotify(context.Background(), dsn, func() {
		once.Do(func() { close(ready) })
	})
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("postgres listen")
	}
}

func dialPlayer(t *testing.T, base, token string) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(base, "http")+"/v1/ws", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer " + token}},
	})
	if err != nil {
		t.Fatalf("dial %d %v", statusOf(resp), err)
	}
	t.Cleanup(func() { conn.Close(websocket.StatusNormalClosure, "") })
	return conn
}

func wantBoth(t *testing.T, a, b *websocket.Conn, typ, saga string, version int) {
	t.Helper()
	for _, conn := range []*websocket.Conn{a, b} {
		ev := readEvent(t, conn)
		if ev.Type != typ || ev.SagaID != saga || ev.Version != version {
			t.Fatalf("got %+v want %s %s v%d", ev, typ, saga, version)
		}
	}
}

func readEvent(t *testing.T, conn *websocket.Conn) realtime.Event {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		ev, err := realtime.Parse(data)
		if err != nil {
			t.Fatal(err, string(data))
		}
		if ev.Type == realtime.TypePing {
			continue
		}
		return ev
	}
	t.Fatal("no event")
	return realtime.Event{}
}

func withNoNotify(t *testing.T, dsn string, fn func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "LISTEN "+realtime.Channel); err != nil {
		t.Fatal(err)
	}
	fn()
	wctx, wcancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer wcancel()
	note, err := conn.WaitForNotification(wctx)
	if err == nil {
		t.Fatalf("unexpected notify %s", note.Payload)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}

func expectQuiet(t *testing.T, conn *websocket.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err == nil {
		t.Fatalf("unexpected frame %s", data)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
