// Package backend is the companion's client for the Sagalore API.
package backend

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/sagalore/sagalore/internal/contract"
	"github.com/sagalore/sagalore/internal/envcompat"
	"github.com/sagalore/sagalore/internal/screenshot"
)

type Client struct {
	BaseURL string
	APIKey  string
	HTTP    *http.Client
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

type HTTPError struct {
	Status  int
	Code    string
	Message string
}

func (e *HTTPError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("API-svar %d", e.Status)
}

func (e *HTTPError) Permanent() bool {
	return e.Status >= 400 && e.Status < 500 && e.Status != http.StatusTooManyRequests && e.Status != http.StatusRequestTimeout
}

// DevLogin exchanges SAGALORE_DEV_TOKEN for a gss_ session. It does not call Battle.net.
// Optional identity comes from SAGALORE_DEV_BATTLETAG / SAGALORE_DEV_BNET_ACCOUNT /
// SAGALORE_DEV_DISPLAY_NAME so a local run can merge onto an existing player.
func (c *Client) DevLogin(ctx context.Context, devToken string) (contract.SessionResponse, error) {
	body := map[string]string{}
	if v := envcompat.Get("SAGALORE_DEV_BATTLETAG"); v != "" {
		body["battletag"] = v
	}
	if v := envcompat.Get("SAGALORE_DEV_BNET_ACCOUNT"); v != "" {
		body["bnet_account_id"] = v
	}
	if v := envcompat.Get("SAGALORE_DEV_DISPLAY_NAME"); v != "" {
		body["display_name"] = v
	}
	raw := []byte(`{}`)
	if len(body) > 0 {
		var err error
		raw, err = json.Marshal(body)
		if err != nil {
			return contract.SessionResponse{}, err
		}
	}
	var out contract.SessionResponse
	err := c.doToken(ctx, devToken, http.MethodPost, "/v1/auth/dev", raw, "application/json", &out)
	return out, err
}

// PollBnet waits on a Battle.net attempt. pending is true while the browser flow is open.
func (c *Client) PollBnet(ctx context.Context, attempt string) (contract.SessionResponse, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/v1/auth/bnet/poll?attempt="+url.QueryEscape(attempt), nil)
	if err != nil {
		return contract.SessionResponse{}, false, err
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return contract.SessionResponse{}, false, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusAccepted {
		return contract.SessionResponse{}, true, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var body contract.ErrorBody
		_ = json.Unmarshal(raw, &body)
		return contract.SessionResponse{}, false, &HTTPError{Status: resp.StatusCode, Code: body.Error, Message: body.Message}
	}
	var out contract.SessionResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return contract.SessionResponse{}, false, err
	}
	return out, false, nil
}

// AcceptInvite joins the signed-in account for one invite.
func (c *Client) AcceptInvite(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/invites/"+url.PathEscape(id)+"/accept", []byte(`{}`), "application/json", nil, nil)
}

// DeclineInvite marks an invite declined. The decision sticks.
func (c *Client) DeclineInvite(ctx context.Context, id string) error {
	return c.do(ctx, http.MethodPost, "/v1/invites/"+url.PathEscape(id)+"/decline", []byte(`{}`), "application/json", nil, nil)
}

func (c *Client) UpsertCharacter(ctx context.Context, req contract.CharacterUpsert) (contract.Character, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return contract.Character{}, err
	}
	var out contract.Character
	err = c.do(ctx, http.MethodPut, "/v1/characters", raw, "application/json", nil, &out)
	return out, err
}

func (c *Client) Sync(ctx context.Context) (contract.SyncResponse, error) {
	var out contract.SyncResponse
	err := c.do(ctx, http.MethodGet, "/v1/sync", nil, "", nil, &out)
	return out, err
}

// ResetProgress deletes API progress for this player. Empty questID clears all.
func (c *Client) ResetProgress(ctx context.Context, questID string) (deleted int64, err error) {
	path := "/v1/progress"
	if q := strings.TrimSpace(questID); q != "" {
		path += "?saga_id=" + url.QueryEscape(q)
	}
	var out struct {
		Deleted int64 `json:"deleted"`
	}
	err = c.do(ctx, http.MethodDelete, path, nil, "", nil, &out)
	return out.Deleted, err
}

// SagaImage downloads a saga cover image for the addon media folder.
func (c *Client) SagaImage(ctx context.Context, questID string) (contentType string, body []byte, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/v1/sagas/"+questID+"/image", nil)
	if err != nil {
		return "", nil, err
	}
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	req.Header.Set("User-Agent", "Sagalore")
	resp, err := c.http().Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
	if err != nil {
		return "", nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", nil, &HTTPError{Status: resp.StatusCode, Message: fmt.Sprintf("image status %d", resp.StatusCode)}
	}
	return resp.Header.Get("Content-Type"), raw, nil
}

func (c *Client) Level(ctx context.Context, req contract.LevelRequest) (contract.LevelResponse, error) {
	raw, err := json.Marshal(req)
	if err != nil {
		return contract.LevelResponse{}, err
	}
	var out contract.LevelResponse
	err = c.do(ctx, http.MethodPost, "/v1/level", raw, "application/json", nil, &out)
	return out, err
}

// Complete uploads the screenshot the player took. The API may notify the group webhook.
func (c *Client) Complete(ctx context.Context, req contract.CompletionRequest, image []byte) (contract.CompletionResponse, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	fields := map[string]string{
		"character_id": req.CharacterID,
		"saga_id":      req.QuestID,
		"saga_version": strconv.Itoa(req.QuestVersion),
		"battletag":    req.BattleTag,
		"level":        strconv.Itoa(req.Level),
		"completed_at": req.CompletedAt,
	}
	if req.StartedAt != "" {
		fields["started_at"] = req.StartedAt
	}
	if req.Race != "" {
		fields["race"] = req.Race
	}
	if req.Class != "" {
		fields["class"] = req.Class
	}
	if req.Instance != "" {
		fields["instance"] = req.Instance
	}
	if len(req.GroupMembers) > 0 {
		raw, err := json.Marshal(req.GroupMembers)
		if err != nil {
			return contract.CompletionResponse{}, err
		}
		fields["group_members"] = string(raw)
	}
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return contract.CompletionResponse{}, err
		}
	}
	contentType := ""
	if len(image) > 0 {
		normalized, ct, err := screenshot.NormalizeUpload(image)
		if err != nil {
			return contract.CompletionResponse{}, err
		}
		contentType = ct
		image = normalized
		fw, err := w.CreateFormFile("screenshot", "screenshot"+screenshot.Extension(ct))
		if err != nil {
			return contract.CompletionResponse{}, err
		}
		if _, err := fw.Write(image); err != nil {
			return contract.CompletionResponse{}, err
		}
	}
	if err := w.Close(); err != nil {
		return contract.CompletionResponse{}, err
	}
	_ = contentType
	var out contract.CompletionResponse
	err := c.do(ctx, http.MethodPost, "/v1/complete", body.Bytes(), w.FormDataContentType(), nil, &out)
	return out, err
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, contentType string, _ any, out any) error {
	return c.doToken(ctx, c.APIKey, method, path, body, contentType, out)
}

func (c *Client) doToken(ctx context.Context, token, method, path string, body []byte, contentType string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(c.BaseURL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	req.Header.Set("User-Agent", "Sagalore")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var eb contract.ErrorBody
		_ = json.Unmarshal(raw, &eb)
		return &HTTPError{Status: resp.StatusCode, Code: eb.Error, Message: eb.Message}
	}
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			return fmt.Errorf("svar fra API: %w", err)
		}
	}
	return nil
}
