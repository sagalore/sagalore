package webhook

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"time"
)

// Result is the outcome of a background delivery.
type Result struct {
	Type string
	Ref  string
	Err  error
}

// Queue delivers events off the request path.
type Queue struct {
	client *Client
	jobs   chan job
	// After runs once delivery finishes. May be nil; must not block long.
	After func(Result)
}

type job struct {
	url   string
	ref   string
	event Event
}

// NewQueue starts one worker so deliveries stay ordered.
func NewQueue(client *Client) *Queue {
	if client == nil {
		client = &Client{}
	}
	q := &Queue{client: client, jobs: make(chan job, 64)}
	go q.loop()
	return q
}

// Enqueue posts ev to url asynchronously. Empty url is a no-op.
// Returns whether the event was accepted onto the queue.
func (q *Queue) Enqueue(url, ref string, ev Event) bool {
	if q == nil || q.client == nil {
		return false
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return false
	}
	select {
	case q.jobs <- job{url: url, ref: ref, event: ev}:
		return true
	default:
		slog.Error("webhook queue full", "type", ev.Type)
		return false
	}
}

func (q *Queue) loop() {
	for j := range q.jobs {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err := q.client.Post(ctx, j.url, j.event)
		cancel()
		if err != nil && !errors.Is(err, ErrNotConfigured) {
			slog.Error("webhook", "type", j.event.Type, "err", err)
		}
		if q.After != nil {
			q.After(Result{Type: j.event.Type, Ref: j.ref, Err: err})
		}
	}
}
