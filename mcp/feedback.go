package mcp

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

func init() {
	SetFeedback(FeedbackFromEnv())
	Register(Tool{
		Name:        "submit_feedback",
		Description: feedbackDescription,
		Schema:      feedbackSchema(),
		Call:        callFeedback,
	})
}

const feedbackDescription = `File a bug or a wish as a GitHub issue on sagalore/sagalore. Use this when the player wants to report something broken or ask for a feature. Nothing is merged from this tool.

Required: kind (bug or idea) and text.
Optional: context (what they were doing). Do not add their BattleTag, email, or account id. Include those only when the user wrote them in the text themselves.

Example: {"kind":"bug","text":"After /reload the Goldshire saga still shows the old step."}

The server uses its own GitHub token. It rate-limits reports and does not attach the signed-in player.`

func feedbackSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"kind":    map[string]any{"type": "string", "enum": []string{"bug", "idea", "wish"}, "description": "bug, idea, or wish."},
			"text":    map[string]any{"type": "string", "description": "What happened or what they want. Max 4000 characters."},
			"context": map[string]any{"type": "string", "description": "Optional extra detail. Max 2000 characters."},
		},
		"required": []string{"kind", "text"},
	}
}

func callFeedback(ctx context.Context, args json.RawMessage) (string, error) {
	var in struct {
		Kind    string `json:"kind"`
		Text    string `json:"text"`
		Context string `json:"context"`
	}
	if len(strings.TrimSpace(string(args))) > 0 {
		if err := json.Unmarshal(args, &in); err != nil {
			return "", fmt.Errorf("arguments must be an object")
		}
	}
	report, err := SanitizeFeedback(in.Kind, in.Text, in.Context)
	if err != nil {
		return "", err
	}
	return currentFeedback().Submit(ctx, report)
}

// Feedback is a cleaned report ready to file. It has no account identity.
type Feedback struct {
	Kind  string
	Title string
	Body  string
}

var (
	secretPattern = regexp.MustCompile(`(?i)\b(?:gst_|gss_|gsr_|ghp_|github_pat_|sk-)[A-Za-z0-9_\-]{8,}\b`)
	spaceRun      = regexp.MustCompile(`[ \t]{2,}`)
)

// SanitizeFeedback checks kind and text, strips control characters and tokens,
// and builds an issue title and body. Text the user wrote, including an email
// or BattleTag they typed, is kept. Nothing is added from the environment.
func SanitizeFeedback(kind, text, extra string) (Feedback, error) {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "bug":
		kind = "bug"
	case "idea", "wish":
		kind = "idea"
	default:
		return Feedback{}, fmt.Errorf("kind must be bug or idea")
	}
	text, err := cleanBlock(text, 4000)
	if err != nil {
		return Feedback{}, fmt.Errorf("text: %w", err)
	}
	if text == "" {
		return Feedback{}, fmt.Errorf("text is required")
	}
	extra, err = cleanBlock(extra, 2000)
	if err != nil {
		return Feedback{}, fmt.Errorf("context: %w", err)
	}
	label := "Bug"
	if kind == "idea" {
		label = "Idea"
	}
	title := label + ": " + firstLine(text)
	if utf8.RuneCountInString(title) > 120 {
		title = string([]rune(title)[:117]) + "..."
	}
	var body strings.Builder
	body.WriteString("Kind: " + kind + "\n\n")
	body.WriteString(text)
	if extra != "" {
		body.WriteString("\n\nContext:\n")
		body.WriteString(extra)
	}
	body.WriteString("\n\nFiled from the Sagalore MCP submit_feedback tool.\n")
	return Feedback{Kind: kind, Title: title, Body: body.String()}, nil
}

func cleanBlock(s string, max int) (string, error) {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	var b strings.Builder
	for _, r := range s {
		if r == '\n' || r == '\t' || (r >= 32 && r != 127) {
			b.WriteRune(r)
		}
	}
	s = secretPattern.ReplaceAllString(b.String(), "[redacted]")
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) > max {
		return "", fmt.Errorf("must be at most %d characters", max)
	}
	return s, nil
}

func firstLine(s string) string {
	line := s
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		line = s[:i]
	}
	line = strings.TrimSpace(spaceRun.ReplaceAllString(line, " "))
	if line == "" {
		return "Feedback"
	}
	return line
}

// FeedbackClient files GitHub issues with a server token.
type FeedbackClient struct {
	Token   string
	Repo    string
	BaseURL string
	HTTP    *http.Client
	Limit   int
	Window  time.Duration
	Now     func() time.Time

	mu   sync.Mutex
	hits map[string][]time.Time
}

var (
	fbMu sync.Mutex
	fb   = &FeedbackClient{}
)

// FeedbackFromEnv reads SAGALORE_GITHUB_TOKEN (or GITHUB_TOKEN) and
// SAGALORE_FEEDBACK_REPO (default sagalore/sagalore).
func FeedbackFromEnv() FeedbackClient {
	token := strings.TrimSpace(os.Getenv("SAGALORE_GITHUB_TOKEN"))
	if token == "" {
		token = strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
	}
	repo := strings.TrimSpace(os.Getenv("SAGALORE_FEEDBACK_REPO"))
	if repo == "" {
		repo = "sagalore/sagalore"
	}
	return FeedbackClient{Token: token, Repo: repo, Limit: 5, Window: time.Hour}
}

// SetFeedback replaces the client used by submit_feedback. Tests use this.
func SetFeedback(c FeedbackClient) {
	if c.Limit <= 0 {
		c.Limit = 5
	}
	if c.Window <= 0 {
		c.Window = time.Hour
	}
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.HTTP == nil {
		c.HTTP = http.DefaultClient
	}
	if strings.TrimSpace(c.Repo) == "" {
		c.Repo = "sagalore/sagalore"
	}
	c.hits = map[string][]time.Time{}
	fbMu.Lock()
	fb = &c
	fbMu.Unlock()
}

func currentFeedback() *FeedbackClient {
	fbMu.Lock()
	defer fbMu.Unlock()
	return fb
}

var repoPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+$`)

// Submit opens one issue. The GitHub token never appears in the issue body.
func (c *FeedbackClient) Submit(ctx context.Context, report Feedback) (string, error) {
	repo := strings.TrimSpace(c.Repo)
	if !repoPattern.MatchString(repo) {
		return "", fmt.Errorf("SAGALORE_FEEDBACK_REPO must look like owner/name")
	}
	if strings.TrimSpace(c.Token) == "" {
		return "", fmt.Errorf("SAGALORE_GITHUB_TOKEN is not set on the server")
	}
	if !c.allow(tokenKey(c.Token)) {
		return "", fmt.Errorf("rate limit: at most %d feedback reports per hour", c.Limit)
	}
	number, htmlURL, err := c.postIssue(ctx, repo, report, true)
	if err != nil {
		return "", err
	}
	if htmlURL == "" {
		htmlURL = "https://github.com/" + repo + "/issues/" + fmt.Sprint(number)
	}
	return fmt.Sprintf("Opened issue #%d: %s", number, htmlURL), nil
}

func tokenKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (c *FeedbackClient) allow(key string) bool {
	now := c.Now()
	if now.IsZero() {
		now = time.Now()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.hits == nil {
		c.hits = map[string][]time.Time{}
	}
	windowStart := now.Add(-c.Window)
	kept := c.hits[key][:0]
	for _, at := range c.hits[key] {
		if at.After(windowStart) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= c.Limit {
		c.hits[key] = kept
		return false
	}
	c.hits[key] = append(kept, now)
	return true
}

func (c *FeedbackClient) postIssue(ctx context.Context, repo string, report Feedback, withLabel bool) (int, string, error) {
	payload := map[string]any{
		"title": report.Title,
		"body":  report.Body,
	}
	if withLabel {
		payload["labels"] = []string{"feedback"}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, "", err
	}
	base := strings.TrimRight(strings.TrimSpace(c.BaseURL), "/")
	if base == "" {
		base = "https://api.github.com"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/repos/"+repo+"/issues", bytes.NewReader(raw))
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "sagalore-mcp")
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnprocessableEntity && withLabel && strings.Contains(strings.ToLower(string(body)), "label") {
		return c.postIssue(ctx, repo, report, false)
	}
	if resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(body))
		if msg == "" {
			msg = resp.Status
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return 0, "", fmt.Errorf("GitHub %d: %s", resp.StatusCode, msg)
	}
	var out struct {
		Number  int    `json:"number"`
		HTMLURL string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return 0, "", err
	}
	return out.Number, out.HTMLURL, nil
}
