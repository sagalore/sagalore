package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ErrNotConfigured means the group has no webhook URL.
var ErrNotConfigured = errors.New("webhook is not configured")

// Client POSTs JSON events to a webhook URL.
type Client struct {
	HTTP    *http.Client
	Timeout time.Duration
}

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	timeout := 15 * time.Second
	if c != nil && c.Timeout > 0 {
		timeout = c.Timeout
	}
	return &http.Client{Timeout: timeout}
}

// Post sends ev as application/json to url. Empty url returns ErrNotConfigured.
func (c *Client) Post(ctx context.Context, url string, ev Event) error {
	url = strings.TrimSpace(url)
	if url == "" {
		return ErrNotConfigured
	}
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return fmt.Errorf("webhook url must be http(s)")
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Sagalore-Webhook/1")
	res, err := c.httpClient().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return fmt.Errorf("webhook status %d", res.StatusCode)
	}
	return nil
}
