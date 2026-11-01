package realtime

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// ClientIdle is how long the companion waits for a data frame (the server's
// text ping counts) before it assumes the socket is dead and reconnects.
const ClientIdle = 70 * time.Second

// Hooks is the companion's WebSocket loop.
type Hooks struct {
	// Endpoint returns the ws URL and the player bearer. Empty waits and retries.
	Endpoint func() (url, token string)
	// OnConnect runs after the handshake, including every reconnect.
	// The companion should GET /v1/sync here. It must return quickly.
	OnConnect func()
	// OnEvent runs for every frame except {"type":"ping"}. It must return quickly.
	OnEvent func(Event)
	// OnUnauthorized runs when the handshake is 401 so the companion can log in again.
	OnUnauthorized func()
	// OnDialError runs when the handshake failed for another reason.
	OnDialError func(error)
	// OnDisconnect runs when a live socket ends.
	OnDisconnect func(error)
}

// Maintain dials, reads, and reconnects until ctx is cancelled.
func Maintain(ctx context.Context, h Hooks) {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		wsURL, token := "", ""
		if h.Endpoint != nil {
			wsURL, token = h.Endpoint()
		}
		if wsURL == "" || token == "" {
			if !sleep(ctx, time.Second) {
				return
			}
			continue
		}
		conn, resp, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
			HTTPHeader: http.Header{
				"Authorization": []string{"Bearer " + token},
				"User-Agent":    []string{"Sagalore"},
			},
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if resp != nil && resp.StatusCode == http.StatusUnauthorized && h.OnUnauthorized != nil {
				h.OnUnauthorized()
			}
			attempt++
			if h.OnDialError != nil {
				h.OnDialError(err)
			}
			if !sleep(ctx, Jitter(Backoff(attempt))) {
				return
			}
			continue
		}
		attempt = 0
		if h.OnConnect != nil {
			h.OnConnect()
		}
		err = readSocket(ctx, conn, h.OnEvent)
		_ = conn.Close(websocket.StatusNormalClosure, "")
		if ctx.Err() != nil {
			return
		}
		if h.OnDisconnect != nil {
			h.OnDisconnect(err)
		}
		attempt++
		if !sleep(ctx, Jitter(Backoff(attempt))) {
			return
		}
	}
}

func readSocket(ctx context.Context, conn *websocket.Conn, on func(Event)) error {
	conn.SetReadLimit(1 << 16)
	for {
		rctx, cancel := context.WithTimeout(ctx, ClientIdle)
		_, data, err := conn.Read(rctx)
		cancel()
		if err != nil {
			return err
		}
		ev, err := Parse(data)
		if err != nil || ev.Type == TypePing || on == nil {
			continue
		}
		on(ev)
	}
}

// WebSocketURL maps an API base (http://localhost:8080 or https://sagalore.example.com)
// to the player socket. http becomes ws, https becomes wss.
func WebSocketURL(apiBase string) string {
	u, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil || u.Host == "" {
		return ""
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
	default:
		return ""
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/ws"
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}
