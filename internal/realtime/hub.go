package realtime

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
)

const (
	// PingInterval is how often a server socket sends a protocol ping and a
	// text {"type":"ping"}. Reverse proxies and NATs often idle-close quiet
	// connections; 25s stays under common ~60s HTTP/1.1 idle limits in case a
	// connection never finished the WebSocket upgrade.
	PingInterval = 25 * time.Second
	writeTimeout = 10 * time.Second
)

// Hub fans events out to the WebSockets held by this process.
type Hub struct {
	mu      sync.Mutex
	clients map[*client]struct{}
	ping    time.Duration
	ctx     context.Context
	cancel  context.CancelFunc
	once    sync.Once
}

type client struct {
	playerID string
	conn     *websocket.Conn
	send     chan Event
	once     sync.Once
}

// NewHub starts idle. Call Listen to attach Postgres, and Serve from GET /v1/ws.
func NewHub() *Hub {
	ctx, cancel := context.WithCancel(context.Background())
	return &Hub{
		clients: map[*client]struct{}{},
		ping:    PingInterval,
		ctx:     ctx,
		cancel:  cancel,
	}
}

// SetPingInterval overrides the heartbeat for tests. Existing sockets keep
// the interval they started with.
func (h *Hub) SetPingInterval(d time.Duration) {
	h.mu.Lock()
	h.ping = d
	h.mu.Unlock()
}

func (h *Hub) pingEvery() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ping <= 0 {
		return PingInterval
	}
	return h.ping
}

// Close stops Listen and closes every socket with 1001 Going Away.
func (h *Hub) Close() {
	h.once.Do(func() { h.cancel() })
}

// Broadcast delivers ev to every matching socket on this process.
// A slow socket is closed so the companion reconnects and runs /v1/sync.
func (h *Hub) Broadcast(ev Event) {
	var slow []*client
	h.mu.Lock()
	for c := range h.clients {
		if !ev.VisibleTo(c.playerID) {
			continue
		}
		select {
		case c.send <- ev:
		default:
			slow = append(slow, c)
		}
	}
	h.mu.Unlock()
	for _, c := range slow {
		c.drop()
	}
}

func (c *client) drop() {
	c.once.Do(func() {
		_ = c.conn.Close(websocket.StatusTryAgainLater, "slow")
	})
}

func (h *Hub) add(c *client) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.ctx.Err() != nil {
		return false
	}
	h.clients[c] = struct{}{}
	return true
}

func (h *Hub) remove(c *client) {
	h.mu.Lock()
	delete(h.clients, c)
	h.mu.Unlock()
}

// Serve upgrades one player socket. playerID is the Sagalore player uuid from
// the session. The handler must authenticate before calling Serve.
// Origin is not checked: this is not a browser, and the bearer token is the
// credential. TLS usually ends at a reverse proxy that forwards the upgrade.
func (h *Hub) Serve(w http.ResponseWriter, r *http.Request, playerID string) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		slog.Warn("websocket", "err", err)
		return
	}
	conn.SetReadLimit(4096)
	ctx, cancel := context.WithCancel(h.ctx)
	defer cancel()
	defer conn.Close(websocket.StatusNormalClosure, "")

	c := &client{playerID: playerID, conn: conn, send: make(chan Event, 32)}
	if !h.add(c) {
		_ = conn.Close(websocket.StatusGoingAway, "shutdown")
		return
	}
	defer h.remove(c)
	slog.Info("websocket", "player", playerID)

	go func() {
		defer cancel()
		for {
			// Ping waits for this Read to observe the pong.
			if _, _, err := conn.Read(ctx); err != nil {
				return
			}
		}
	}()

	ping := time.NewTicker(h.pingEvery())
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = conn.Close(websocket.StatusGoingAway, "shutdown")
			return
		case <-ping.C:
			if err := writePing(ctx, conn); err != nil {
				return
			}
		case ev := <-c.send:
			if err := writeEvent(ctx, conn, ev); err != nil {
				return
			}
		}
	}
}

func writePing(ctx context.Context, conn *websocket.Conn) error {
	pctx, cancel := context.WithTimeout(ctx, writeTimeout)
	err := conn.Ping(pctx)
	cancel()
	if err != nil {
		return err
	}
	body, err := (Event{Type: TypePing}).Bytes()
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, body)
}

func writeEvent(ctx context.Context, conn *websocket.Conn, ev Event) error {
	body, err := ev.Bytes()
	if err != nil {
		return err
	}
	wctx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return conn.Write(wctx, websocket.MessageText, body)
}
