package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientPostJSON(t *testing.T) {
	var got Event
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type %q", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	ev := Event{
		Type:       TypeSagaCreated,
		OccurredAt: time.Date(2026, 10, 31, 12, 0, 0, 0, time.UTC),
		Group:      GroupRef{ID: "g1", Slug: "tutorial", Name: "Tutorial"},
		Saga:       &SagaRef{ID: "s1", Title: "Vakt", Version: 1, Active: true},
	}
	if err := (&Client{HTTP: srv.Client()}).Post(context.Background(), srv.URL, ev); err != nil {
		t.Fatal(err)
	}
	if got.Type != TypeSagaCreated || got.Saga == nil || got.Saga.Title != "Vakt" {
		t.Fatalf("%+v", got)
	}
}

func TestClientRejectsEmptyURL(t *testing.T) {
	err := (&Client{}).Post(context.Background(), "", Event{Type: TypeSagaCreated})
	if err != ErrNotConfigured {
		t.Fatalf("got %v", err)
	}
}

func TestClientRetries429ThenSucceeds(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := hits.Add(1)
		if n == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	err := (&Client{HTTP: srv.Client(), Attempts: 3}).Post(context.Background(), srv.URL, Event{
		Type:  TypeSagaCompleted,
		Group: GroupRef{Slug: "gutar"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() != 2 {
		t.Fatalf("hits=%d", hits.Load())
	}
}

func TestClientNoRetryOn400(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(srv.Close)

	err := (&Client{HTTP: srv.Client(), Attempts: 5}).Post(context.Background(), srv.URL, Event{Type: TypeSagaCreated})
	if err == nil {
		t.Fatal("expected error")
	}
	if hits.Load() != 1 {
		t.Fatalf("hits=%d", hits.Load())
	}
}

func TestParseRetryAfter(t *testing.T) {
	if d := parseRetryAfter("2"); d != 2*time.Second {
		t.Fatalf("seconds: %v", d)
	}
	if d := parseRetryAfter("99"); d != 30*time.Second {
		t.Fatalf("cap: %v", d)
	}
	if d := parseRetryAfter(""); d != 0 {
		t.Fatalf("empty: %v", d)
	}
}

func TestQueueDelivers(t *testing.T) {
	done := make(chan Event, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var ev Event
		_ = json.NewDecoder(r.Body).Decode(&ev)
		done <- ev
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	q := NewQueue(&Client{HTTP: srv.Client()})
	q.MinInterval = -1 // no spacing for a fast unit test
	if !q.Enqueue(srv.URL, "ref-1", Event{Type: TypeInviteAccepted, Group: GroupRef{Slug: "g"}}) {
		t.Fatal("enqueue")
	}
	select {
	case ev := <-done:
		if ev.Type != TypeInviteAccepted {
			t.Fatalf("%+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

func TestQueueSpacesDeliveries(t *testing.T) {
	times := make(chan time.Time, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		times <- time.Now()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(srv.Close)

	q := NewQueue(&Client{HTTP: srv.Client()})
	q.MinInterval = 200 * time.Millisecond
	_ = q.Enqueue(srv.URL, "a", Event{Type: TypeSagaCreated})
	_ = q.Enqueue(srv.URL, "b", Event{Type: TypeSagaCreated})

	t0 := <-times
	t1 := <-times
	if gap := t1.Sub(t0); gap < 180*time.Millisecond {
		t.Fatalf("gap %v too small", gap)
	}
}
