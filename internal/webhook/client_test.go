package webhook

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
