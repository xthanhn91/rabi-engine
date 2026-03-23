package tools

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// DispatchTaskTool wraps agents/cto/scripts/dispatch-task.sh as a native tool.
// Dispatches tasks to claude -p for a given project, tracks state, supports retry.
type DispatchTaskTool struct {
	scriptsDir string
}

func NewDispatchTaskTool(scriptsDir string) *DispatchTaskTool {
	return &DispatchTaskTool{scriptsDir: scriptsDir}
}

func (t *DispatchTaskTool) Name() string { return "dispatch_task" }

func (t *DispatchTaskTool) Description() string {
	return `Dispatch a task to a dev agent (Claude Code) for a specific project.
Tracks task state, supports status checks and retries.

WHEN TO USE:
- User asks to fix something: "sửa cái bug này đi", "fix lỗi ở project X"
- User wants to delegate work: "dispatch task", "giao việc cho dev", "chạy task"
- User says "giao việc X cho Y": X=prompt (task instructions), Y=project name → resolve path from project-registry.json
- User asks to implement a feature: "thêm feature Y vào project Z"
- Checking dispatched task results: "task đó xong chưa?", "kết quả task?"
- Retrying a failed task: "chạy lại task đó"

WHEN NOT TO USE:
- Just checking project status (use scan_fleet instead)
- Running health checks (use health_check instead)
- Quick one-off commands on a project (use exec instead)

ACTIONS:
There are 3 modes, selected by parameters:

1. DISPATCH (default): Run a new task
   - project_path (required): Absolute path to the project directory
   - prompt (required): Task instructions for the dev agent
   - skill (optional): Skill name to activate (e.g. "fix", "test", "review")
   - max_turns (optional): Max agent turns before stopping (default: 10)

2. STATUS: Check task results
   - action: "status"
   - task_id (optional): Specific task ID. If omitted, shows all recent tasks.

3. RETRY: Re-dispatch a failed task
   - action: "retry"
   - task_id (required): ID of the task to retry

BEHAVIOR:
- Generates unique task ID (task-YYYYMMDD-HHMMSS-hex)
- Records task state to ~/.rabi-cto/tasks/ before running
- Runs claude -p with JSON output and parses result
- Updates task record to completed/failed after execution
- Timeout: 600 seconds (10 minutes)

TIPS:
- Always check scan_fleet or health_check first to identify WHICH project needs work
- Use skill="fix" for bug fixes, skill="test" for test writing
- Use status action to check all dispatched tasks before dispatching new ones`
}

func (t *DispatchTaskTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"action": map[string]any{
				"type":        "string",
				"description": "Action mode: 'dispatch' (default), 'status', or 'retry'",
				"enum":        []string{"dispatch", "status", "retry"},
			},
			"project_path": map[string]any{
				"type":        "string",
				"description": "Absolute path to the project directory (required for dispatch)",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "Task instructions for the dev agent (required for dispatch)",
			},
			"skill": map[string]any{
				"type":        "string",
				"description": "Skill to activate for the task (e.g. 'fix', 'test', 'review')",
			},
			"max_turns": map[string]any{
				"type":        "integer",
				"description": "Max agent turns before stopping (default: 10)",
			},
			"task_id": map[string]any{
				"type":        "string",
				"description": "Task ID for status check or retry",
			},
		},
	}
}

func (t *DispatchTaskTool) Execute(ctx context.Context, args map[string]any) *Result {
	scriptPath := filepath.Join(t.scriptsDir, "dispatch-task.sh")

	action, _ := args["action"].(string)
	if action == "" {
		action = "dispatch"
	}

	var cmdArgs []string

	switch action {
	case "status":
		cmdArgs = append(cmdArgs, "--status")
		if taskID, _ := args["task_id"].(string); taskID != "" {
			cmdArgs = append(cmdArgs, taskID)
		}

	case "retry":
		taskID, _ := args["task_id"].(string)
		if taskID == "" {
			return ErrorResult("task_id is required for retry action")
		}
		cmdArgs = append(cmdArgs, "--retry", taskID)

	case "dispatch":
		projectPath, _ := args["project_path"].(string)
		prompt, _ := args["prompt"].(string)
		if projectPath == "" || prompt == "" {
			return ErrorResult("project_path and prompt are required for dispatch action")
		}
		cmdArgs = append(cmdArgs, projectPath, prompt)
		if skill, _ := args["skill"].(string); skill != "" {
			cmdArgs = append(cmdArgs, "--skill", skill)
		}
		if maxTurns, ok := args["max_turns"].(float64); ok && maxTurns > 0 {
			cmdArgs = append(cmdArgs, "--max-turns", strconv.Itoa(int(maxTurns)))
		}

	default:
		return ErrorResult(fmt.Sprintf("unknown action: %s (valid: dispatch, status, retry)", action))
	}

	ctx, cancel := context.WithTimeout(ctx, 600*time.Second)
	defer cancel()

	cmd := exec.CommandContext(ctx, "bash", append([]string{scriptPath}, cmdArgs...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return ErrorResult(fmt.Sprintf("dispatch_task failed: %s\n%s", err, stderr.String()))
	}

	return SilentResult(stdout.String())
}
