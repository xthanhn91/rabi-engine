// Package facebook provides an HTTP client for the Facebook sidecar API.
// The sidecar (Python FastAPI) handles browser automation via Camoufox.
// This client is used by FacebookTool to expose FB actions to the LLM.
package facebook

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"
)

// SidecarClient communicates with the Python Facebook sidecar via HTTP.
type SidecarClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewSidecarClient creates a client with 90s timeout (browser ops are slow).
func NewSidecarClient(baseURL string) *SidecarClient {
	return &SidecarClient{
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 90 * time.Second},
	}
}

// post sends a JSON POST request and returns the parsed response.
func (c *SidecarClient) post(ctx context.Context, path string, body any) (map[string]any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+path, bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sidecar unreachable: %w", err)
	}
	defer resp.Body.Close()

	// HTTP 503 = checkpoint detected
	if resp.StatusCode == http.StatusServiceUnavailable {
		return nil, fmt.Errorf("checkpoint_detected: manual intervention required")
	}

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// get sends a GET request with query parameters.
func (c *SidecarClient) get(ctx context.Context, path string, params map[string]string) (map[string]any, error) {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return nil, err
	}
	q := u.Query()
	for k, v := range params {
		q.Set(k, v)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sidecar unreachable: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	var result map[string]any
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// --- Typed methods (thin wrappers) ---

func (c *SidecarClient) Login(ctx context.Context, accountID, email, password string) (map[string]any, error) {
	return c.post(ctx, "/api/login", map[string]string{
		"account_id": accountID, "email": email, "password": password,
	})
}

func (c *SidecarClient) PostFeed(ctx context.Context, accountID, text, imagePath string) (map[string]any, error) {
	body := map[string]string{"account_id": accountID, "text": text}
	if imagePath != "" {
		body["image_path"] = imagePath
	}
	return c.post(ctx, "/api/post/feed", body)
}

func (c *SidecarClient) PostGroup(ctx context.Context, accountID, groupURL, text, imagePath string) (map[string]any, error) {
	body := map[string]string{"account_id": accountID, "group_url": groupURL, "text": text}
	if imagePath != "" {
		body["image_path"] = imagePath
	}
	return c.post(ctx, "/api/post/group", body)
}

func (c *SidecarClient) ReplyComment(ctx context.Context, accountID, postURL, replyText, commentID string) (map[string]any, error) {
	body := map[string]string{"account_id": accountID, "post_url": postURL, "reply_text": replyText}
	if commentID != "" {
		body["comment_id"] = commentID
	}
	return c.post(ctx, "/api/comment/reply", body)
}

func (c *SidecarClient) Like(ctx context.Context, accountID, targetURL, targetType, commentID string) (map[string]any, error) {
	body := map[string]string{"account_id": accountID, "target_url": targetURL, "target_type": targetType}
	if commentID != "" {
		body["comment_id"] = commentID
	}
	return c.post(ctx, "/api/like", body)
}

func (c *SidecarClient) ReadFeed(ctx context.Context, accountID string, scrollCount int) (map[string]any, error) {
	return c.get(ctx, "/api/feed", map[string]string{
		"account_id":   accountID,
		"scroll_count": fmt.Sprintf("%d", scrollCount),
	})
}

func (c *SidecarClient) ReadNotifications(ctx context.Context, accountID string) (map[string]any, error) {
	return c.get(ctx, "/api/notifications", map[string]string{"account_id": accountID})
}

func (c *SidecarClient) Status(ctx context.Context, accountID string) (map[string]any, error) {
	params := map[string]string{}
	if accountID != "" {
		params["account_id"] = accountID
	}
	return c.get(ctx, "/api/status", params)
}

func (c *SidecarClient) Screenshot(ctx context.Context, accountID string) (map[string]any, error) {
	return c.post(ctx, "/api/screenshot", map[string]string{"account_id": accountID})
}

// HealthCheck returns true if the sidecar is reachable.
func (c *SidecarClient) HealthCheck(ctx context.Context) bool {
	result, err := c.get(ctx, "/api/status", nil)
	if err != nil {
		slog.Warn("facebook sidecar health check failed", "error", err)
		return false
	}
	ok, _ := result["ok"].(bool)
	return ok
}
