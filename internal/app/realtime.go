package app

import (
	"context"

	"github.com/sagalore/sagalore/internal/realtime"
)

// watchRealtime keeps the player socket open. Events and reconnects ask the
// main loop to sync. The callbacks only signal; they do not call the API.
func (a *App) watchRealtime(ctx context.Context, wake, unauthorized chan struct{}) {
	realtime.Maintain(ctx, realtime.Hooks{
		Endpoint: func() (string, string) {
			a.mu.Lock()
			defer a.mu.Unlock()
			if a.Client == nil {
				return "", ""
			}
			return realtime.WebSocketURL(a.Client.BaseURL), a.Client.APIKey
		},
		OnConnect: func() { signal(wake) },
		OnEvent: func(ev realtime.Event) {
			if ev.Version > 0 {
				a.Log.Info("sanntid", "type", ev.Type, "saga_id", ev.SagaID, "version", ev.Version)
			} else {
				a.Log.Info("sanntid", "type", ev.Type, "player", ev.PlayerID)
			}
			signal(wake)
		},
		OnUnauthorized: func() { signal(unauthorized) },
		OnDialError: func(err error) {
			if ctx.Err() == nil {
				a.Log.Warn("sanntid ikke tilkoblet", "err", err)
			}
		},
		OnDisconnect: func(err error) {
			if ctx.Err() == nil {
				a.Log.Warn("sanntid frakoblet", "err", err)
			}
		},
	})
}

func signal(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
