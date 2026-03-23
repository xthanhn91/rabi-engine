package tools

import (
	"context"
	"fmt"
)

// ZaloReplySender abstracts the Zalo Personal channel operations needed by Zalo tools.
// Breaks the import cycle: tools ← channels/zalo/personal.
type ZaloReplySender interface {
	IsRunning() bool
	// FindContact looks up a contact by display name (case-insensitive, partial match).
	FindContact(name string) (uid string, displayName string, found bool)
	// SendText sends a text message to a Zalo user by UID.
	SendText(ctx context.Context, uid, message string) error
	// Reconnect deletes saved credentials and re-authenticates (triggers QR flow).
	Reconnect(ctx context.Context) error
}

// ZaloSendTool sends a text message to a Zalo contact by name.
type ZaloSendTool struct {
	sender ZaloReplySender
}

func NewZaloSendTool(sender ZaloReplySender) *ZaloSendTool {
	return &ZaloSendTool{sender: sender}
}

func (t *ZaloSendTool) Name() string { return "zalo_send" }

func (t *ZaloSendTool) Description() string {
	return `Send a text message to a Zalo contact by name.

Use this tool when anh asks to reply, respond, or send a message to someone on Zalo.
Messages prefixed "💬 Zalo |" are relayed from Zalo — use this tool to reply.

Parameters:
- name: Contact display name (partial match OK). Example: "Hùng"
- message: Text to send

If Zalo is disconnected, this tool will report it. Do NOT auto-reconnect — inform the user instead.`
}

func (t *ZaloSendTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Contact display name to send to (partial match OK)",
			},
			"message": map[string]any{
				"type":        "string",
				"description": "Message text to send",
			},
		},
		"required": []string{"name", "message"},
	}
}

func (t *ZaloSendTool) Execute(ctx context.Context, args map[string]any) *Result {
	name, _ := args["name"].(string)
	message, _ := args["message"].(string)
	if name == "" || message == "" {
		return ErrorResult("both 'name' and 'message' are required")
	}
	if !t.sender.IsRunning() {
		return ErrorResult("Zalo is disconnected. Inform the user that Zalo is currently offline.")
	}
	uid, displayName, found := t.sender.FindContact(name)
	if !found {
		return ErrorResult(fmt.Sprintf("contact not found: %q", name))
	}
	if err := t.sender.SendText(ctx, uid, message); err != nil {
		return ErrorResult(fmt.Sprintf("failed to send to %s: %v", displayName, err))
	}
	return SilentResult(fmt.Sprintf("Sent to %s via Zalo", displayName))
}

// ZaloReconnectTool forces a Zalo re-login by generating a new QR code.
// WARNING: This requires the user to physically scan the QR code with their phone.
// ONLY call this when the user EXPLICITLY requests reconnection.
type ZaloReconnectTool struct {
	sender ZaloReplySender
}

func NewZaloReconnectTool(sender ZaloReplySender) *ZaloReconnectTool {
	return &ZaloReconnectTool{sender: sender}
}

func (t *ZaloReconnectTool) Name() string { return "zalo_reconnect" }

func (t *ZaloReconnectTool) Description() string {
	return `Force re-login Zalo by generating a new QR code.

WARNING: This triggers a QR code that requires PHYSICAL PHONE SCANNING with the Zalo app.
This is DISRUPTIVE — the user must have their phone ready.

ONLY call this tool when the user EXPLICITLY says one of:
- "kết nối lại Zalo"
- "QR mới"
- "reconnect Zalo"
- "Zalo expired"

NEVER call this just because zalo_send failed. If send fails due to disconnection,
report it and wait for the user to explicitly request reconnection.`
}

func (t *ZaloReconnectTool) Parameters() map[string]any {
	return map[string]any{
		"type":       "object",
		"properties": map[string]any{},
	}
}

func (t *ZaloReconnectTool) Execute(ctx context.Context, _ map[string]any) *Result {
	if err := t.sender.Reconnect(ctx); err != nil {
		return ErrorResult(fmt.Sprintf("Zalo reconnect failed: %v", err))
	}
	return SilentResult("Zalo reconnecting — QR code sent via Telegram. Ask user to scan with Zalo app.")
}
