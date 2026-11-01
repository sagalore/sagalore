package realtime

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

func TestParseAndVisibility(t *testing.T) {
	ev, err := Parse([]byte(`{"type":"saga.updated","saga_id":"q1","version":4}`))
	if err != nil || ev.Type != TypeUpdated || ev.SagaID != "q1" || ev.Version != 4 || ev.PlayerID != "" {
		t.Fatalf("%+v %v", ev, err)
	}
	if !ev.VisibleTo("anyone") {
		t.Fatal("catalog events are for every player")
	}
	level, err := Parse([]byte(`{"type":"level.changed","player_id":"p1"}`))
	if err != nil || level.Version != 0 || level.SagaID != "" {
		t.Fatal(level, err)
	}
	if !level.VisibleTo("p1") || level.VisibleTo("p2") {
		t.Fatal("level events stay with that player")
	}
	raw, err := (Event{Type: TypePing}).Bytes()
	if err != nil || string(raw) != `{"type":"ping"}` {
		t.Fatalf("%s %v", raw, err)
	}
	created, _ := (Event{Type: TypeCreated, SagaID: "q", Version: 1}).Bytes()
	if string(created) != `{"type":"saga.created","saga_id":"q","version":1}` {
		t.Fatal(string(created))
	}
}

func TestBackoff(t *testing.T) {
	if Backoff(0) != time.Second || Backoff(1) != time.Second || Backoff(2) != 2*time.Second || Backoff(3) != 4*time.Second {
		t.Fatalf("%s %s %s", Backoff(1), Backoff(2), Backoff(3))
	}
	if Backoff(7) != 60*time.Second || Backoff(12) != 60*time.Second {
		t.Fatal(Backoff(7), Backoff(12))
	}
	for i := 0; i < 20; i++ {
		j := Jitter(time.Second)
		if j < 200*time.Millisecond || j > 2*time.Second {
			t.Fatal(j)
		}
	}
}

func TestWebSocketURL(t *testing.T) {
	if got := WebSocketURL("http://localhost:8080"); got != "ws://localhost:8080/v1/ws" {
		t.Fatal(got)
	}
	if got := WebSocketURL("https://sagalore.example.com/"); got != "wss://sagalore.example.com/v1/ws" {
		t.Fatal(got)
	}
	if WebSocketURL("") != "" || WebSocketURL("ftp://x") != "" {
		t.Fatal("bad url")
	}
}

func TestHubPingShutdownAndEvents(t *testing.T) {
	h := NewHub()
	h.SetPingInterval(20 * time.Millisecond)
	t.Cleanup(h.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		player := "p1"
		if r.URL.Query().Get("as") == "p2" {
			player = "p2"
		}
		h.Serve(w, r, player)
	}))
	t.Cleanup(srv.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	u := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/ws"
	c1, _, err := websocket.Dial(ctx, u, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer secret"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c1.Close(websocket.StatusNormalClosure, "") })
	c2, _, err := websocket.Dial(ctx, u+"?as=p2", &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": []string{"Bearer secret"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c2.Close(websocket.StatusNormalClosure, "") })

	ev := readUntil(t, c1, func(ev Event) bool { return ev.Type == TypePing })
	if ev.Type != TypePing {
		t.Fatal(ev)
	}
	h.Broadcast(Event{Type: TypeLevel, PlayerID: "p1"})
	h.Broadcast(Event{Type: TypeCreated, SagaID: "saga", Version: 2})
	got := readUntil(t, c1, func(ev Event) bool { return ev.Type == TypeCreated })
	if got.SagaID != "saga" || got.Version != 2 {
		t.Fatal(got)
	}
	other := readUntil(t, c2, func(ev Event) bool { return ev.Type != TypePing })
	if other.Type != TypeCreated {
		t.Fatalf("player 2 should see the saga, not the level: %+v", other)
	}

	h.Close()
	rctx, rcancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer rcancel()
	if _, _, err := c1.Read(rctx); err == nil {
		t.Fatal("expected close on shutdown")
	}
}

func TestMaintainReconnectsOnEventAndRejectsUnauthorized(t *testing.T) {
	h := NewHub()
	h.SetPingInterval(time.Hour)
	t.Cleanup(h.Close)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gss_ok" {
			http.Error(w, "no", http.StatusUnauthorized)
			return
		}
		h.Serve(w, r, "p1")
	}))
	t.Cleanup(srv.Close)
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/v1/ws"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var mu sync.Mutex
	var got []Event
	connected := make(chan struct{}, 4)
	unauth := make(chan struct{}, 1)
	var token atomic.Value
	token.Store("gss_ok")
	go Maintain(ctx, Hooks{
		Endpoint: func() (string, string) { return wsURL, token.Load().(string) },
		OnConnect: func() {
			select {
			case connected <- struct{}{}:
			default:
			}
		},
		OnEvent: func(ev Event) {
			mu.Lock()
			got = append(got, ev)
			mu.Unlock()
		},
		OnUnauthorized: func() {
			select {
			case unauth <- struct{}{}:
			default:
			}
		},
	})
	select {
	case <-connected:
	case <-time.After(2 * time.Second):
		t.Fatal("connect")
	}
	time.Sleep(100 * time.Millisecond)
	h.Broadcast(Event{Type: TypePing})
	h.Broadcast(Event{Type: TypeCreated, SagaID: "q", Version: 1})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	gotCopy := append([]Event(nil), got...)
	mu.Unlock()
	if len(gotCopy) != 1 || gotCopy[0].Type != TypeCreated || gotCopy[0].SagaID != "q" || gotCopy[0].Version != 1 {
		t.Fatalf("%+v", gotCopy)
	}

	token.Store("nope")
	h.Close()
	select {
	case <-unauth:
	case <-time.After(3 * time.Second):
		t.Fatal("expected 401 on reconnect")
	}
	cancel()
}

func readUntil(t *testing.T, conn *websocket.Conn, match func(Event) bool) Event {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		_, data, err := conn.Read(ctx)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		ev, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		if match(ev) {
			return ev
		}
	}
	t.Fatal("timeout")
	return Event{}
}
