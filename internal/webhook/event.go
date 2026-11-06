// Package webhook delivers group notification events as JSON POSTs.
// Payloads are host-agnostic; Discord formatting belongs outside this repo.
package webhook

import "time"

// Event types delivered to a group's webhook URL.
const (
	TypeSagaCreated    = "saga.created"
	TypeSagaUpdated    = "saga.updated"
	TypeSagaRetired    = "saga.retired"
	TypeSagaCompleted  = "saga.completed"
	TypeInviteAccepted = "invite.accepted"
	TypeJoinJoined     = "join.joined"    // open join link redeemed
	TypeJoinRequested  = "join.requested" // gated request submitted
	TypeJoinApproved   = "join.approved"  // gated request approved
	TypeJoinRejected   = "join.rejected"  // gated request rejected
)

// Event is the JSON body POSTed to a group webhook.
type Event struct {
	Type       string     `json:"type"`
	OccurredAt time.Time  `json:"occurred_at"`
	Group      GroupRef   `json:"group"`
	Saga       *SagaRef   `json:"saga,omitempty"`
	Invite     *InviteRef `json:"invite,omitempty"`
	Join       *JoinRef   `json:"join,omitempty"`
	Actor      *ActorRef  `json:"actor,omitempty"`
	Character  *CharRef   `json:"character,omitempty"`
	Characters []CharRef  `json:"characters,omitempty"`
	Changes    []string   `json:"changes,omitempty"`
}

// GroupRef identifies the group that owns the webhook.
type GroupRef struct {
	ID   string `json:"id"`
	Slug string `json:"slug"`
	Name string `json:"name,omitempty"`
}

// SagaRef is the saga involved in a saga.* event.
type SagaRef struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Version     int    `json:"version"`
	Description string `json:"description,omitempty"`
	Active      bool   `json:"active"`
}

// InviteRef is set on invite.accepted.
type InviteRef struct {
	ID string `json:"id"`
}

// JoinRef is set on join.* events from shareable join links.
type JoinRef struct {
	ID     string `json:"id,omitempty"` // request id (gated); empty for open joins
	LinkID string `json:"link_id,omitempty"`
	Mode   string `json:"mode,omitempty"`   // open | gated
	Status string `json:"status,omitempty"` // pending | approved | rejected
}

// ActorRef is who caused the event when known (BattleTag name, display name, …).
type ActorRef struct {
	Name      string `json:"name,omitempty"`
	BattleTag string `json:"battletag,omitempty"`
}

// CharRef is an in-game character.
type CharRef struct {
	Name  string `json:"name"`
	Realm string `json:"realm,omitempty"`
	Race  string `json:"race,omitempty"`
	Class string `json:"class,omitempty"`
	Level int    `json:"level,omitempty"`
}
