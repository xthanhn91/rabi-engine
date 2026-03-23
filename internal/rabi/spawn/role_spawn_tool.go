// Package spawn provides a Rabi-specific spawn tool wrapper that injects
// role context files (SOUL.md, AGENTS.md) into subagent task descriptions.
//
// This wrapper sits on top of the standard GoClaw SpawnTool without modifying it.
// Convention: roles/{type}/SOUL.md + roles/{type}/AGENTS.md at workspace root.
package spawn

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// RoleSpawnTool wraps the standard SpawnTool to inject role-specific context.
// When LLM calls spawn(type="cto", task="..."), this wrapper:
// 1. Reads roles/cto/SOUL.md + roles/cto/AGENTS.md from workspace
// 2. Prepends the content to the task description
// 3. Delegates to the standard SpawnTool
type RoleSpawnTool struct {
	inner     tools.Tool // standard SpawnTool
	workspace string     // workspace root for resolving role file paths
}

// NewRoleSpawnTool wraps an existing SpawnTool with role context injection.
func NewRoleSpawnTool(inner tools.Tool, workspace string) *RoleSpawnTool {
	return &RoleSpawnTool{
		inner:     inner,
		workspace: workspace,
	}
}

func (t *RoleSpawnTool) Name() string        { return t.inner.Name() }
func (t *RoleSpawnTool) Description() string { return t.inner.Description() }

// Parameters extends the standard spawn tool parameters with a "type" field.
func (t *RoleSpawnTool) Parameters() map[string]any {
	params := t.inner.Parameters()

	// Add "type" parameter to the properties map
	if props, ok := params["properties"].(map[string]any); ok {
		props["type"] = map[string]any{
			"type":        "string",
			"description": "Named role type (e.g. 'cto', 'chro', 'cfo', 'cmo'). Injects role-specific SOUL.md and AGENTS.md into the subagent's context.",
		}
	}

	return params
}

// Execute intercepts spawn calls to inject role context into the task.
func (t *RoleSpawnTool) Execute(ctx context.Context, args map[string]any) *tools.Result {
	typeName, _ := args["type"].(string)

	// If a role type is specified, read and prepend role context files
	if typeName != "" {
		roleContext := t.readRoleContext(typeName)
		if roleContext != "" {
			task, _ := args["task"].(string)
			// Prepend role context to task so the subagent receives role identity
			args["task"] = fmt.Sprintf("[Role: %s]\n%s\n[/Role Context]\n\n%s", typeName, roleContext, task)
			slog.Info("rabi: role context injected into spawn", "type", typeName)
		}
		// Remove "type" from args so the standard SpawnTool doesn't see an unknown parameter
		delete(args, "type")
	}

	return t.inner.Execute(ctx, args)
}

// readRoleContext reads SOUL.md and AGENTS.md from roles/{typeName}/ directory.
// Returns concatenated content or empty string if files don't exist.
func (t *RoleSpawnTool) readRoleContext(typeName string) string {
	roleDir := filepath.Join(t.workspace, "roles", typeName)

	var parts []string
	for _, filename := range []string{"SOUL.md", "AGENTS.md"} {
		fullPath := filepath.Join(roleDir, filename)
		data, err := os.ReadFile(fullPath)
		if err != nil {
			slog.Debug("rabi: role context file not found", "path", fullPath)
			continue
		}
		if len(data) > 0 {
			parts = append(parts, string(data))
		}
	}

	return strings.Join(parts, "\n\n---\n\n")
}

// SetContext delegates to inner tool if it supports ContextualTool.
func (t *RoleSpawnTool) SetContext(channel, chatID string) {
	if ct, ok := t.inner.(tools.ContextualTool); ok {
		ct.SetContext(channel, chatID)
	}
}

// SetPeerKind delegates to inner tool if it supports PeerKindAware.
func (t *RoleSpawnTool) SetPeerKind(peerKind string) {
	if pk, ok := t.inner.(tools.PeerKindAware); ok {
		pk.SetPeerKind(peerKind)
	}
}
