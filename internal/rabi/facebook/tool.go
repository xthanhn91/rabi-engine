package facebook

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// FacebookTool exposes the facebook_action tool to the LLM agent loop.
// Maps action parameter to sidecar HTTP endpoints.
// Credentials come from config — LLM never needs to know email/password.
type FacebookTool struct {
	client    *SidecarClient
	accountID string
	email     string
	password  string
}

// NewFacebookTool creates the tool with a sidecar client and pre-configured credentials.
func NewFacebookTool(sidecarURL, accountID, email, password string) *FacebookTool {
	return &FacebookTool{
		client:    NewSidecarClient(sidecarURL),
		accountID: accountID,
		email:     email,
		password:  password,
	}
}

// Client exposes the sidecar client for health checks at startup.
func (t *FacebookTool) Client() *SidecarClient { return t.client }

func (t *FacebookTool) Name() string { return "facebook_action" }

func (t *FacebookTool) Description() string {
	desc := `Interact with Facebook via browser automation sidecar.
Actions: login, post_feed, post_group, reply_comment, like, read_feed, read_notifications, screenshot, status.
Always check "status" first to verify session is active. If not, call "login" first.
Credentials are pre-configured — just call login with no email/password needed.
Reading actions (read_feed, read_notifications, status) have no budget cost.
Mutating actions (post, like, reply) are rate-limited (max 30/day).
If error contains "checkpoint_detected": STOP all Facebook actions and notify user.
If error contains "budget_exceeded": wait before retrying.`
	if t.accountID != "" {
		desc += "\nDefault account_id: " + t.accountID
	}
	return desc
}

func (t *FacebookTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"enum":        []string{"login", "post_feed", "post_group", "reply_comment", "like", "read_feed", "read_notifications", "screenshot", "status"},
				"description": "The Facebook action to perform",
			},
			"account_id": map[string]any{
				"type":        "string",
				"description": "Facebook account profile ID (matches profiles/ directory)",
			},
			"text": map[string]any{
				"type":        "string",
				"description": "Post or comment text (for post_feed, post_group, reply_comment)",
			},
			"target_url": map[string]any{
				"type":        "string",
				"description": "Facebook URL of post or group to interact with",
			},
			"comment_id": map[string]any{
				"type":        "string",
				"description": "Specific comment ID for reply_comment or like_comment",
			},
			"image_path": map[string]any{
				"type":        "string",
				"description": "Absolute path to image file for post_feed or post_group",
			},
			"scroll_count": map[string]any{
				"type":        "integer",
				"description": "Number of scroll steps for read_feed (default 3)",
			},
			"email":    map[string]any{"type": "string", "description": "Facebook email (login only, first time)"},
			"password": map[string]any{"type": "string", "description": "Facebook password (login only, first time)"},
		},
		"required": []string{"action", "account_id"},
	}
}

// ensureLoggedIn always calls /api/login before mutating actions.
// The login endpoint is idempotent — checks is_logged_in() first, skips if already logged in.
// We can't rely on session_active (it only means browser context exists, not FB-logged-in).
func (t *FacebookTool) ensureLoggedIn(ctx context.Context, accountID string) error {
	if t.email == "" || t.password == "" {
		return fmt.Errorf("no credentials configured for auto-login")
	}
	result, err := t.client.Login(ctx, accountID, t.email, t.password)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	if ok, _ := result["ok"].(bool); !ok {
		errMsg, _ := result["error"].(string)
		return fmt.Errorf("login failed: %s", errMsg)
	}
	return nil
}

func (t *FacebookTool) Execute(ctx context.Context, args map[string]any) *tools.Result {
	action, _ := args["action"].(string)
	accountID, _ := args["account_id"].(string)
	// Auto-fill from config if not provided
	if accountID == "" {
		accountID = t.accountID
	}
	if accountID == "" {
		return &tools.Result{IsError: true, ForLLM: "account_id is required"}
	}

	text, _ := args["text"].(string)
	targetURL, _ := args["target_url"].(string)
	commentID, _ := args["comment_id"].(string)
	imagePath, _ := args["image_path"].(string)
	email, _ := args["email"].(string)
	password, _ := args["password"].(string)
	// Auto-fill credentials from config for login action
	if action == "login" {
		if email == "" {
			email = t.email
		}
		if password == "" {
			password = t.password
		}
	}
	scrollCount := 3
	if sc, ok := args["scroll_count"].(float64); ok {
		scrollCount = int(sc)
	}

	var (
		result map[string]any
		err    error
	)

	// Auto-login before mutating actions: check status, login if needed.
	needsSession := action == "post_feed" || action == "post_group" || action == "reply_comment" || action == "like"
	if needsSession {
		if loginErr := t.ensureLoggedIn(ctx, accountID); loginErr != nil {
			return &tools.Result{IsError: true, ForLLM: fmt.Sprintf("auto-login failed: %v", loginErr)}
		}
	}

	switch action {
	case "login":
		result, err = t.client.Login(ctx, accountID, email, password)
	case "post_feed":
		result, err = t.client.PostFeed(ctx, accountID, text, imagePath)
	case "post_group":
		result, err = t.client.PostGroup(ctx, accountID, targetURL, text, imagePath)
	case "reply_comment":
		result, err = t.client.ReplyComment(ctx, accountID, targetURL, text, commentID)
	case "like":
		result, err = t.client.Like(ctx, accountID, targetURL, "post", commentID)
	case "read_feed":
		result, err = t.client.ReadFeed(ctx, accountID, scrollCount)
	case "read_notifications":
		result, err = t.client.ReadNotifications(ctx, accountID)
	case "status":
		result, err = t.client.Status(ctx, accountID)
	case "screenshot":
		result, err = t.client.Screenshot(ctx, accountID)
	default:
		return &tools.Result{IsError: true, ForLLM: fmt.Sprintf("unknown action: %s", action)}
	}

	if err != nil {
		return &tools.Result{IsError: true, ForLLM: err.Error()}
	}

	// Check sidecar-level error
	if ok, _ := result["ok"].(bool); !ok {
		errMsg, _ := result["error"].(string)
		if errMsg == "" {
			errMsg = "sidecar returned ok=false"
		}
		return &tools.Result{IsError: true, ForLLM: errMsg}
	}

	out, _ := json.Marshal(result)
	return &tools.Result{ForLLM: string(out)}
}
