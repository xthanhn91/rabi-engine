package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"
)

// ScanFleetTool wraps agents/cto/scripts/scan-projects.sh as a native tool.
// Scans all registered projects for git changes and returns JSON status.
type ScanFleetTool struct {
	scriptsDir string
}

func NewScanFleetTool(scriptsDir string) *ScanFleetTool {
	return &ScanFleetTool{scriptsDir: scriptsDir}
}

func (t *ScanFleetTool) Name() string { return "scan_fleet" }

func (t *ScanFleetTool) Description() string {
	return `Scan all registered projects for git changes, open PRs, and recent commits.
Returns a JSON array with each project's status: branch, uncommitted files, open PRs, last commit.

WHEN TO USE:
- User asks about project status: "tình hình dự án sao rồi?", "có gì mới không?", "dự án nào có thay đổi?"
- User asks for fleet overview: "scan fleet", "check projects", "project status"
- User wants to know what changed: "cái nào đang làm dở?", "có PR nào không?"
- Before deciding which project to work on
- During daily/weekly reviews

WHEN NOT TO USE:
- Looking at a SINGLE project's code (use exec + git commands instead)
- Checking health/CI status (use health_check instead)
- Dispatching a task to a project (use dispatch_task instead)

PARAMETERS:
- show_all (boolean, optional): Include projects WITHOUT changes. Default: only projects with changes.
  Vietnamese triggers for show_all=true: "hết", "tất cả", "toàn bộ", "all", "hết đi", "scan hết"

BEHAVIOR:
- Scans projects in parallel (up to 5 concurrent) for speed
- Checks: git status, recent commits, open PRs via gh CLI
- Output: JSON array on stdout, log messages on stderr
- Timeout: 120 seconds
- Projects without .git directory are skipped with a warning

TIPS:
- Use without show_all first to see what needs attention
- Use with show_all=true for a complete fleet inventory`
}

func (t *ScanFleetTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"show_all": map[string]any{
				"type":        "boolean",
				"description": "Include projects without changes (default: false, only show projects with changes)",
			},
		},
	}
}

func (t *ScanFleetTool) Execute(ctx context.Context, args map[string]any) *Result {
	scriptPath := filepath.Join(t.scriptsDir, "scan-projects.sh")

	var cmdArgs []string
	if showAll, _ := args["show_all"].(bool); showAll {
		cmdArgs = append(cmdArgs, "--all")
	}

	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", append([]string{scriptPath}, cmdArgs...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return ErrorResult(fmt.Sprintf("scan_fleet failed: %s\n%s", err, stderr.String()))
	}

	return SilentResult(stdout.String())
}
