package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
)

// API is the Sagalore HTTP API the tools call.
type API struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

var (
	apiMu sync.Mutex
	api   = API{}
)

func init() {
	SetAPI(APIFromEnv())
}

// APIFromEnv reads SAGALORE_API_URL (default https://api.sagalore.app) and SAGALORE_TOKEN.
func APIFromEnv() API {
	base := strings.TrimRight(strings.TrimSpace(os.Getenv("SAGALORE_API_URL")), "/")
	if base == "" {
		base = "https://api.sagalore.app"
	}
	return API{BaseURL: base, Token: strings.TrimSpace(os.Getenv("SAGALORE_TOKEN"))}
}

// SetAPI replaces the client used by tools. Tests use this.
func SetAPI(next API) {
	if next.HTTP == nil {
		next.HTTP = http.DefaultClient
	}
	if next.BaseURL == "" {
		next.BaseURL = "https://api.sagalore.app"
	}
	apiMu.Lock()
	api = next
	apiMu.Unlock()
}

func currentAPI() API {
	apiMu.Lock()
	defer apiMu.Unlock()
	return api
}

func (a API) call(ctx context.Context, method, path string, body any) (string, error) {
	if strings.TrimSpace(a.Token) == "" {
		return "", fmt.Errorf("SAGALORE_TOKEN is not set. After Battle.net login, mint a personal token with POST /v1/me/tokens and restart this server with it")
	}
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return "", err
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.BaseURL+path, rdr)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+a.Token)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	text := strings.TrimSpace(string(raw))
	if resp.StatusCode >= 300 {
		if text == "" {
			text = resp.Status
		}
		return "", fmt.Errorf("API %d: %s", resp.StatusCode, text)
	}
	if text == "" {
		text = "ok"
	}
	return text, nil
}
