package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

// HealthCheckTool wraps agents/cto/scripts/health-check.sh as a native tool.
// Runs 5 health checks per project and assigns a 1-10 score.
type HealthCheckTool struct {
	scriptsDir string
}

func NewHealthCheckTool(scriptsDir string) *HealthCheckTool {
	return &HealthCheckTool{scriptsDir: scriptsDir}
}

func (t *HealthCheckTool) Name() string { return "health_check" }

func (t *HealthCheckTool) Description() string {
	return `Run health checks on fleet projects: CI status, stale PRs, uncommitted work, doc staleness.
Scores each project 1-10 and categorizes: healthy (8-10), warning (5-7), critical (1-4).

WHEN TO USE:
- User asks about project health: "cái nào đang lỗi?", "dự án nào có vấn đề?", "CI sao rồi?"
- User asks for health overview: "health check", "kiểm tra sức khỏe", "tình trạng hệ thống"
- User asks about CI/CD: "build có pass không?", "test có lỗi không?"
- User wants to find troubled projects: "cái nào cần sửa?", "đâu đang critical?"
- During morning standup or weekly review

WHEN NOT TO USE:
- Just checking what changed (use scan_fleet instead)
- Dispatching a fix task (use dispatch_task instead)
- Debugging a specific CI failure (use exec + gh CLI instead)

PARAMETERS:
- project (string, optional): Check a single project by name. Default: check all projects.
- dry_run (boolean, optional): Skip Telegram notification, just return JSON. Default: false.

BEHAVIOR:
- 5 checks per project: uncommitted files, stale branches, stale PRs, CI status, doc staleness
- Score deductions: CI failure (-3), >10 uncommitted (-3), stale PRs (-2), stale branches (-2)
- Sends formatted Telegram notification unless dry_run=true
- Output: JSON with healthy/warning/critical counts + per-project details
- Timeout: 180 seconds

TIPS:
- Use dry_run=true when just checking status without notifying
- Use project="name" to drill into a specific project`
}

func (t *HealthCheckTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"project": map[string]any{
				"type":        "string",
				"description": "Check a single project by name (default: all projects)",
			},
			"dry_run": map[string]any{
				"type":        "boolean",
				"description": "Skip Telegram notification, just return JSON result (default: false)",
			},
		},
	}
}

func (t *HealthCheckTool) Execute(ctx context.Context, args map[string]any) *Result {
	scriptPath := filepath.Join(t.scriptsDir, "health-check.sh")

	var cmdArgs []string
	if project, _ := args["project"].(string); project != "" {
		cmdArgs = append(cmdArgs, "--project", project)
	}
	if dryRun, _ := args["dry_run"].(bool); dryRun {
		cmdArgs = append(cmdArgs, "--dry-run")
	}

	ctx, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", append([]string{scriptPath}, cmdArgs...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return ErrorResult(fmt.Sprintf("health_check failed: %s\n%s", err, stderr.String()))
	}

	return SilentResult(stdout.String())
}
