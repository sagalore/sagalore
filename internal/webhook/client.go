package webhook

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrNotConfigured means the group has no webhook URL.
var ErrNotConfigured = errors.New("webhook is not configured")

const (
	defaultTimeout    = 15 * time.Second
	defaultAttempts   = 5
	defaultMaxBackoff = 8 * time.Second
)

// Client POSTs JSON events to a webhook URL.
type Client struct {
	HTTP *http.Client
	// Timeout is per attempt. Zero uses 15s.
	Timeout time.Duration
	// Attempts is how many tries on 429/5xx or transport errors. Zero uses 5.
	Attempts int
}

func (c *Client) httpClient() *http.Client {
	if c != nil && c.HTTP != nil {
		return c.HTTP
	}
	timeout := defaultTimeout
	if c != nil && c.Timeout > 0 {
		timeout = c.Timeout
	}
	return &http.Client{Timeout: timeout}
}

func (c *Client) attempts() int {
	if c == nil || c.Attempts == 0 {
		return defaultAttempts
	}
	if c.Attempts < 1 {
		return 1
	}
	return c.Attempts
}

// Post sends ev as application/json to url. Empty url returns ErrNotConfigured.
// On HTTP 429, 5xx, or a transport error it retries with backoff (honours
// Retry-After when present, capped at 30s).
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

	var lastErr error
	var retryAfter time.Duration
	n := c.attempts()
	for attempt := 0; attempt < n; attempt++ {
		if attempt > 0 {
			wait := retryAfter
			if wait <= 0 {
				wait = backoffFor(attempt)
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				timer.Stop()
				if lastErr != nil {
					return lastErr
				}
				return ctx.Err()
			case <-timer.C:
			}
		}

		status, nextWait, err := c.doOnce(ctx, url, body)
		if err == nil {
			return nil
		}
		lastErr = err
		retryAfter = nextWait
		if status != 0 && !retryableStatus(status) {
			return err
		}
	}
	return lastErr
}

func (c *Client) doOnce(ctx context.Context, url string, body []byte) (status int, retryAfter time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return 0, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Sagalore-Webhook/1")
	res, err := c.httpClient().Do(req)
	if err != nil {
		return 0, 0, err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 200 && res.StatusCode < 300 {
		return res.StatusCode, 0, nil
	}
	return res.StatusCode, parseRetryAfter(res.Header.Get("Retry-After")), fmt.Errorf("webhook status %d", res.StatusCode)
}

func retryableStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func backoffFor(attempt int) time.Duration {
	// attempt is 1..n-1 when waiting after a failure.
	if attempt < 1 {
		attempt = 1
	}
	d := time.Duration(1<<uint(attempt-1)) * time.Second
	if d > defaultMaxBackoff {
		return defaultMaxBackoff
	}
	return d
}

func parseRetryAfter(raw string) time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if secs, err := strconv.Atoi(raw); err == nil {
		if secs <= 0 {
			return 0
		}
		d := time.Duration(secs) * time.Second
		if d > 30*time.Second {
			return 30 * time.Second
		}
		return d
	}
	if t, err := http.ParseTime(raw); err == nil {
		d := time.Until(t)
		if d <= 0 {
			return 0
		}
		if d > 30*time.Second {
			return 30 * time.Second
		}
		return d
	}
	return 0
}
