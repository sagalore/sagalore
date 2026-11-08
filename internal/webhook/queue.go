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

const (
	defaultMinInterval = 750 * time.Millisecond
	defaultJobTimeout  = 90 * time.Second
)

// Queue delivers events off the request path.
type Queue struct {
	client *Client
	jobs   chan job
	// MinInterval is the minimum gap between deliveries. Zero uses 750ms.
	// Negative disables spacing.
	MinInterval time.Duration
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

func (q *Queue) minInterval() time.Duration {
	if q == nil {
		return defaultMinInterval
	}
	if q.MinInterval < 0 {
		return 0
	}
	if q.MinInterval == 0 {
		return defaultMinInterval
	}
	return q.MinInterval
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
	var lastAt time.Time
	for j := range q.jobs {
		if gap := q.minInterval(); gap > 0 && !lastAt.IsZero() {
			if wait := gap - time.Since(lastAt); wait > 0 {
				time.Sleep(wait)
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), defaultJobTimeout)
		err := q.client.Post(ctx, j.url, j.event)
		cancel()
		lastAt = time.Now()
		if err != nil && !errors.Is(err, ErrNotConfigured) {
			slog.Error("webhook", "type", j.event.Type, "err", err)
		}
		if q.After != nil {
			q.After(Result{Type: j.event.Type, Ref: j.ref, Err: err})
		}
	}
}
