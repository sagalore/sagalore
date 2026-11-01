package realtime

import (
	"context"
	"log/slog"
	"time"

	"github.com/jackc/pgx/v5"
)

// Listen subscribes this process to Channel until Close or ctx is cancelled.
// ready, when set, runs after each successful LISTEN (including reconnects).
// The connection is dedicated. A pooled connection would be reused and drop
// the subscription.
func (h *Hub) Listen(ctx context.Context, databaseURL string) {
	h.ListenNotify(ctx, databaseURL, nil)
}

// ListenNotify is Listen with a hook for tests.
func (h *Hub) ListenNotify(parent context.Context, databaseURL string, ready func()) {
	if databaseURL == "" {
		slog.Error("realtime listen", "err", "DATABASE_URL is empty")
		return
	}
	ctx, cancel := context.WithCancel(parent)
	defer cancel()
	go func() {
		select {
		case <-h.ctx.Done():
			cancel()
		case <-ctx.Done():
		}
	}()
	attempt := 0
	for {
		if ctx.Err() != nil {
			return
		}
		err := h.listenOnce(ctx, databaseURL, ready)
		if ctx.Err() != nil {
			return
		}
		attempt++
		wait := Backoff(attempt)
		slog.Error("realtime listen", "err", err, "retry", wait.String())
		if !sleep(ctx, wait) {
			return
		}
	}
}

func (h *Hub) listenOnce(ctx context.Context, databaseURL string, ready func()) error {
	cfg, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return err
	}
	if cfg.RuntimeParams == nil {
		cfg.RuntimeParams = map[string]string{}
	}
	cfg.RuntimeParams["application_name"] = "sagalore-realtime"
	conn, err := pgx.ConnectConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, "LISTEN "+Channel); err != nil {
		return err
	}
	if ready != nil {
		ready()
	}
	for {
		note, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		if note.Channel != Channel {
			continue
		}
		ev, err := Parse([]byte(note.Payload))
		if err != nil {
			slog.Warn("realtime payload", "err", err)
			continue
		}
		h.Broadcast(ev)
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}
