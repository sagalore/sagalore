// Package realtime is the push channel between the Sagalore API and the companion.
// The API writes one small JSON event per saga change. The companion fetches
// GET /v1/sync itself. Postgres LISTEN/NOTIFY carries the event to every API
// process, so more than one API replica can hold WebSockets.
package realtime

import "encoding/json"

// Channel is the Postgres NOTIFY channel. Payload is an Event, at most a few
// hundred bytes (NOTIFY's limit is 8000).
const Channel = "sagalore_events"

const (
	TypeCreated   = "saga.created"
	TypeUpdated   = "saga.updated"
	TypeRetired   = "saga.retired"
	TypeCompleted = "saga.completed"
	TypeLevel     = "level.changed"
	TypePing      = "ping"
)

// Event is one WebSocket text frame.
//
//	{"type":"saga.created","saga_id":"<uuid>","version":1}
//	{"type":"saga.updated","saga_id":"<uuid>","version":2}
//	{"type":"saga.retired","saga_id":"<uuid>","version":3}
//	{"type":"saga.completed","saga_id":"<uuid>","version":1}
//	{"type":"level.changed","player_id":"<uuid>"}
//	{"type":"ping"}
//
// player_id is an audience filter. Empty means every connected companion.
// level.changed is the only event that sets it. The frame is a hint: the
// companion loads the saga with GET /v1/sync.
type Event struct {
	Type     string `json:"type"`
	SagaID   string `json:"saga_id,omitempty"`
	Version  int    `json:"version,omitempty"`
	PlayerID string `json:"player_id,omitempty"`
}

// Parse decodes one NOTIFY payload or WebSocket frame.
func Parse(payload []byte) (Event, error) {
	var ev Event
	if err := json.Unmarshal(payload, &ev); err != nil {
		return Event{}, err
	}
	return ev, nil
}

// Bytes encodes the frame sent to companions.
func (e Event) Bytes() ([]byte, error) {
	return json.Marshal(e)
}

// VisibleTo reports whether this player's socket should see the event.
func (e Event) VisibleTo(playerID string) bool {
	if e.Type == TypePing {
		return true
	}
	if e.PlayerID == "" {
		return true
	}
	return e.PlayerID == playerID
}
