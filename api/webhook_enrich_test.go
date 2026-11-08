package main

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/webhook"
)

func TestWebhookSagaIncludesObjectivesAndImageURL(t *testing.T) {
	resetDB(t)
	sink := newWebhookSink(t)
	store := NewStore(testPool)
	const secret = "webhook-image-secret"
	srv := httptest.NewServer((&Server{
		store:          store,
		adminToken:     "admin-test",
		webhooks:       sink.queue,
		publicBaseURL:  "https://sagalore.example.com",
		imageURLSecret: secret,
		imageURLTTL:    time.Hour,
	}).Handler())
	t.Cleanup(srv.Close)

	playerRec, token := devSession(t, "bnet-magnus", "Magnus#2231", "Magnus")
	group := devGroup(t, playerRec.ID, "tutorial", "Tutorial")
	if err := store.SetGroupWebhook(context.Background(), playerRec.ID, group.ID, sink.url); err != nil {
		t.Fatal(err)
	}
	author := apiClient{base: srv.URL, token: token, http: srv.Client()}

	pngBytes := tinyPNG(t)
	var quest contract.Quest
	resp, err := author.do(http.MethodPost, "/v1/sagas", map[string]any{
		"title": "Goldshire evening", "description": "Visit the inn.",
		"objectives": []map[string]any{
			{"id": "go-inn", "type": "go", "text": "Walk to the inn", "zone": "Elwynn Forest"},
			{"id": "talk", "type": "talk", "text": "Talk to the innkeeper", "npc": "Innkeeper Farley"},
		},
		"party":        true,
		"image_base64": base64.StdEncoding.EncodeToString(pngBytes),
	}, &quest)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("create %d %v", statusOf(resp), err)
	}
	if !quest.HasImage {
		t.Fatal("expected cover on created saga")
	}

	created := sink.waitType(t, webhook.TypeSagaCreated)
	if created.Saga == nil {
		t.Fatal("missing saga on webhook")
	}
	if len(created.Saga.Objectives) != 2 {
		t.Fatalf("objectives %+v", created.Saga.Objectives)
	}
	if created.Saga.Objectives[0].Text != "Walk to the inn" || created.Saga.Objectives[1].ID != "talk" {
		t.Fatalf("objectives %+v", created.Saga.Objectives)
	}
	if !strings.HasPrefix(created.Saga.ImageURL, "https://sagalore.example.com/v1/public/sagas/"+quest.ID+"/image?") {
		t.Fatalf("image_url %q", created.Saga.ImageURL)
	}
	if !strings.Contains(created.Saga.ImageURL, "sig=") {
		t.Fatalf("image_url missing sig %q", created.Saga.ImageURL)
	}
}
