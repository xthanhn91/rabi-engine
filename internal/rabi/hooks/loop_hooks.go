// Package hooks — loop_hooks.go provides LoopHookRunner for the agent loop lifecycle.
//
// SEPARATE from Engine/HookConfig (delegation quality gates) because:
// - Exit codes: tri-state (0=inject, 1=skip, 2=block) vs binary (pass/fail)
// - Timeouts: 5s default vs 60s
// - Result: stdout injection vs pass/fail feedback
// - State: tracks editCount for counter-based hooks
package hooks

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/config"
)

// LoopHookEvent identifies when a hook fires in the agent loop lifecycle.
type LoopHookEvent string

const (
	HookPreRun    LoopHookEvent = "pre_run"    // Before every Run()
	HookPostRun   LoopHookEvent = "post_run"   // After every Run()
	HookPreTool   LoopHookEvent = "pre_tool"   // Before tool execution
	HookPostTool  LoopHookEvent = "post_tool"  // After tool execution
	HookPreSpawn  LoopHookEvent = "pre_spawn"  // Before subagent spawn
	HookPostSpawn LoopHookEvent = "post_spawn" // After subagent completes
)

const (
	loopHookDefaultTimeout = 5    // seconds
	loopHookMaxTimeout     = 30   // seconds
	loopHookMaxOutput      = 2000 // chars — prevent prompt bloat
	loopHookMaxPerEvent    = 10   // max hooks per event type
)

// Exit code contract for loop hooks:
//
//	0 = SUCCESS: capture stdout for injection
//	1 = ERROR: log warning + skip (graceful degradation)
//	2 = BLOCK: deny the operation, stderr = reason
const exitCodeBlock = 2

// lifecycleBypassTools are tools that must never be blocked by hooks.
// Blocking these would break context lifecycle (agent stuck forever).
var lifecycleBypassTools = map[string]bool{
	"continue_session": true,
}

// LoopHookRunner executes hooks at agent loop lifecycle points.
// Thread-safe: editCount protected by mutex for parallel tool execution.
// When no hooks are configured, all methods are no-ops (zero allocation).
type LoopHookRunner struct {
	hooks     []config.LoopHookDef
	workspace string
	editCount int
	mu        sync.Mutex
	noop      bool // true = no hooks configured, all methods return early
}

// NewLoopHookRunner creates a runner from config hooks.
// Returns a no-op runner when hooks is empty (all methods return early).
func NewLoopHookRunner(hooks []config.LoopHookDef, workspace string) *LoopHookRunner {
	return &LoopHookRunner{
		hooks:     hooks,
		workspace: workspace,
		noop:      len(hooks) == 0,
	}
}

// RunHooks filters by event+tool, executes matching hooks, returns combined output.
//
// Returns:
//   - output: combined stdout from all matching hooks (newline-separated)
//   - blocked: true if any hook exited with code 2
//   - blockReason: stderr from the blocking hook
//   - err: only for internal errors (not hook failures — those are logged+skipped)
//
// Execution order follows config array order (JSON arrays preserve order in Go).
func (r *LoopHookRunner) RunHooks(ctx context.Context, event LoopHookEvent, toolName string, env map[string]string) (output string, blocked bool, blockReason string, err error) {
	if r.noop {
		return "", false, "", nil
	}

	// Lifecycle bypass: never block critical tools
	if event == HookPreTool && lifecycleBypassTools[toolName] {
		return "", false, "", nil
	}

	eventStr := string(event)
	var outputs []string
	totalStart := time.Now()

	for _, hook := range r.hooks {
		if hook.Event != eventStr {
			continue
		}

		// Tool filter: if hook specifies a tool, only fire for that tool
		if hook.Tool != "" && hook.Tool != toolName {
			continue
		}

		hookOutput, hookBlocked, hookBlockReason, hookErr := r.runSingleHook(ctx, hook, event, toolName, env)
		if hookErr != nil {
			slog.Warn("hooks.loop.error", "event", eventStr, "tool", toolName, "command", hook.Command, "error", hookErr)
			continue // graceful degradation
		}

		if hookBlocked {
			return "", true, hookBlockReason, nil
		}

		if hookOutput != "" {
			outputs = append(outputs, hookOutput)
		}
	}

	// Warn if total hook execution for this event exceeded 15s
	if totalDur := time.Since(totalStart); totalDur > 15*time.Second {
		slog.Warn("hooks.loop.slow_event", "event", eventStr, "total_duration_ms", totalDur.Milliseconds())
	}

	if len(outputs) > 0 {
		return strings.Join(outputs, "\n"), false, "", nil
	}
	return "", false, "", nil
}

// runSingleHook executes one hook command and interprets the exit code.
func (r *LoopHookRunner) runSingleHook(ctx context.Context, hook config.LoopHookDef, event LoopHookEvent, toolName string, extraEnv map[string]string) (output string, blocked bool, blockReason string, err error) {
	// Resolve timeout
	timeout := hook.Timeout
	if timeout <= 0 {
		timeout = loopHookDefaultTimeout
	}
	if timeout > loopHookMaxTimeout {
		timeout = loopHookMaxTimeout
	}

	hookCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Second)
	defer cancel()

	start := time.Now()

	cmd := exec.CommandContext(hookCtx, "sh", "-c", hook.Command)
	cmd.Dir = r.workspace

	// Build environment: base + hook-specific + caller-provided
	r.mu.Lock()
	editCount := r.editCount
	r.mu.Unlock()

	cmd.Env = append(cmd.Environ(),
		"HOOK_EVENT="+string(event),
		"HOOK_TOOL="+toolName,
		"HOOK_WORKSPACE="+r.workspace,
		fmt.Sprintf("HOOK_EDIT_COUNT=%d", editCount),
	)

	// Add caller-provided env vars (e.g., HOOK_TOOL_ARGS)
	for k, v := range extraEnv {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	dur := time.Since(start)

	// Determine exit code
	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			// Not an exit error (timeout, signal, etc.) — treat as error (skip)
			slog.Debug("hooks.loop.run", "event", string(event), "tool", toolName,
				"command", hook.Command, "exit_code", -1, "duration_ms", dur.Milliseconds(),
				"error", runErr.Error())
			return "", false, "", runErr
		}
	}

	// Truncate output to prevent prompt bloat
	out := truncateOutput(stdout.String(), loopHookMaxOutput)

	slog.Debug("hooks.loop.run", "event", string(event), "tool", toolName,
		"command", hook.Command, "exit_code", exitCode, "duration_ms", dur.Milliseconds(),
		"output_len", len(out))

	switch exitCode {
	case 0:
		// Success: return stdout for injection
		return strings.TrimSpace(out), false, "", nil

	case exitCodeBlock:
		// Block: deny the operation, stderr has the reason
		reason := strings.TrimSpace(stderr.String())
		if reason == "" {
			reason = "blocked by hook: " + hook.Command
		}
		slog.Warn("hooks.loop.blocked", "event", string(event), "tool", toolName,
			"command", hook.Command, "reason", reason)
		return "", true, reason, nil

	default:
		// Error (exit 1 or other): log + skip, graceful degradation
		slog.Warn("hooks.loop.skip", "event", string(event), "tool", toolName,
			"command", hook.Command, "exit_code", exitCode)
		return "", false, "", nil
	}
}

// IncrementEditCount bumps the edit counter (thread-safe).
// Called by loop.go after edit/write_file tool execution.
func (r *LoopHookRunner) IncrementEditCount() {
	if r.noop {
		return
	}
	r.mu.Lock()
	r.editCount++
	r.mu.Unlock()
}

// EditCount returns the current edit count (for testing).
func (r *LoopHookRunner) EditCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.editCount
}

// truncateOutput limits string to maxChars (rune count) to prevent prompt bloat.
// Uses rune-aware slicing to avoid cutting multi-byte UTF-8 characters (Vietnamese, emoji).
func truncateOutput(s string, maxChars int) string {
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + "\n[truncated]"
}

// FilterSafetyHooks returns only hooks that enforce safety constraints.
// Used for subagent hook inheritance: subagents inherit blocking pre_tool hooks
// (security gates) but NOT pre_run hooks (fleet status irrelevant for subagents).
func FilterSafetyHooks(hooks []config.LoopHookDef) []config.LoopHookDef {
	var filtered []config.LoopHookDef
	for _, h := range hooks {
		if h.Event == "pre_tool" && h.Inject == "block" {
			filtered = append(filtered, h)
		}
	}
	return filtered
}

// ValidateLoopHooks checks hook config for errors at load time.
// Returns nil if valid, error describing the problem otherwise.
func ValidateLoopHooks(hooks []config.LoopHookDef) error {
	// Count hooks per event
	counts := make(map[string]int)
	for _, h := range hooks {
		counts[h.Event]++
		if counts[h.Event] > loopHookMaxPerEvent {
			return fmt.Errorf("too many hooks for event %q: %d (max %d)", h.Event, counts[h.Event], loopHookMaxPerEvent)
		}
		if h.Command == "" {
			return fmt.Errorf("hook for event %q has empty command", h.Event)
		}
		if h.Timeout > loopHookMaxTimeout {
			return fmt.Errorf("hook for event %q has timeout %ds (max %ds)", h.Event, h.Timeout, loopHookMaxTimeout)
		}
	}
	return nil
}
