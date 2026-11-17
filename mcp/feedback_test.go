package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSanitizeFeedbackKeepsWhatTheUserWrote(t *testing.T) {
	got, err := SanitizeFeedback("wish", "Please add a map pin.\nMy email is player@example.com and my tag is Mira#1234. Token ghp_abcdefghijklmnopqrstuvwxyz", "was in Goldshire")
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != "idea" {
		t.Fatalf("kind %s", got.Kind)
	}
	if !strings.Contains(got.Body, "player@example.com") || !strings.Contains(got.Body, "Mira#1234") {
		t.Fatalf("user-written identity should stay:\n%s", got.Body)
	}
	if strings.Contains(got.Body, "ghp_") || strings.Contains(got.Title, "ghp_") {
		t.Fatalf("token leaked:\n%s", got.Body)
	}
	if strings.Contains(got.Body, "Owner#") || strings.Contains(strings.ToLower(got.Body), "battletag:") {
		t.Fatal("body added an identity the user did not write")
	}
	if _, err := SanitizeFeedback("nope", "text", ""); err == nil {
		t.Fatal("bad kind")
	}
	if _, err := SanitizeFeedback("bug", "  ", ""); err == nil {
		t.Fatal("empty text")
	}
	long := strings.Repeat("a", 4001)
	if _, err := SanitizeFeedback("bug", long, ""); err == nil {
		t.Fatal("overlong text")
	}
}

func TestSubmitFeedbackCreatesLabelledIssue(t *testing.T) {
	var gotPath, gotAuth, gotLabel string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Fatal(err)
		}
		labels, _ := gotBody["labels"].([]any)
		if len(labels) == 1 {
			gotLabel, _ = labels[0].(string)
		}
		if strings.Contains(string(raw), "ghp_") || strings.Contains(string(raw), "server-token") {
			t.Fatalf("secret in body %s", raw)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":42,"html_url":"https://github.com/sagalore/sagalore/issues/42"}`))
	}))
	defer srv.Close()

	SetFeedback(FeedbackClient{
		Token: "server-token-value", Repo: "sagalore/sagalore", BaseURL: srv.URL,
		HTTP: srv.Client(), Limit: 5,
	})

	tool, ok := findTool("submit_feedback")
	if !ok || !strings.Contains(tool.Description, "Example") {
		t.Fatal("submit_feedback missing an example")
	}
	out, err := tool.Call(context.Background(), []byte(`{"kind":"bug","text":"Slash command did nothing after reload."}`))
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/repos/sagalore/sagalore/issues" {
		t.Fatal(gotPath)
	}
	if gotAuth != "Bearer server-token-value" {
		t.Fatal("auth header was not the server token")
	}
	if gotLabel != "feedback" {
		t.Fatalf("label %q", gotLabel)
	}
	body, _ := gotBody["body"].(string)
	if strings.Contains(body, "@") || strings.Contains(strings.ToLower(body), "battletag") {
		t.Fatalf("unexpected identity in %s", body)
	}
	if !strings.Contains(out, "https://github.com/sagalore/sagalore/issues/42") || !strings.Contains(out, "#42") {
		t.Fatal(out)
	}
}

func TestSubmitFeedbackRateLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"number":1,"html_url":"https://github.com/sagalore/sagalore/issues/1"}`))
	}))
	defer srv.Close()
	now := time.Date(2026, 11, 17, 12, 0, 0, 0, time.UTC)
	SetFeedback(FeedbackClient{
		Token: "server-token", Repo: "sagalore/sagalore", BaseURL: srv.URL, HTTP: srv.Client(),
		Limit: 2, Window: time.Hour, Now: func() time.Time { return now },
	})
	tool, _ := findTool("submit_feedback")
	args := []byte(`{"kind":"idea","text":"A fishing saga type."}`)
	if _, err := tool.Call(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	if _, err := tool.Call(context.Background(), args); err != nil {
		t.Fatal(err)
	}
	_, err := tool.Call(context.Background(), args)
	if err == nil || !strings.Contains(err.Error(), "rate limit") {
		t.Fatal(err)
	}
}

func TestSubmitFeedbackMissingToken(t *testing.T) {
	SetFeedback(FeedbackClient{Token: "", Repo: "sagalore/sagalore"})
	tool, _ := findTool("submit_feedback")
	_, err := tool.Call(context.Background(), []byte(`{"kind":"bug","text":"The log is blank."}`))
	if err == nil || !strings.Contains(err.Error(), "SAGALORE_GITHUB_TOKEN") {
		t.Fatal(err)
	}
}
