package roles

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

// Process represents a running child engine process.
type Process interface {
	PID() int
	Wait() error      // blocks until process exits (safe to call multiple times)
	Terminate() error // sends SIGINT for graceful shutdown
	Kill() error      // sends SIGKILL for forced termination
}

// ProcessLauncher creates child engine processes.
// Abstracted as an interface so tests can use a fake implementation.
type ProcessLauncher interface {
	Launch(ctx context.Context, args []string, env []string) (Process, error)
}

// osExecLauncher launches real OS processes using the current binary.
type osExecLauncher struct {
	logDir string // base directory for role log files
}

// NewOSExecLauncher creates a launcher that spawns child processes using os/exec.
// logDir is the base directory for per-role log files (e.g. ~/.goclaw/rabi-roles/).
func NewOSExecLauncher(logDir string) ProcessLauncher {
	return &osExecLauncher{logDir: logDir}
}

func (l *osExecLauncher) Launch(ctx context.Context, args []string, env []string) (Process, error) {
	binary, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve executable path: %w", err)
	}

	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), env...)

	// Extract role name from GOCLAW_ROLE_NAME env var for log file path
	roleName := extractEnvValue(env, "GOCLAW_ROLE_NAME")
	if roleName == "" {
		roleName = "unknown"
	}
	logFile := l.openLogFile(roleName)
	if logFile != nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}

	if err := cmd.Start(); err != nil {
		if logFile != nil {
			logFile.Close()
		}
		return nil, fmt.Errorf("start child process: %w", err)
	}

	return &osProcess{cmd: cmd, logFile: logFile}, nil
}

// openLogFile creates/opens the log file for a role at {logDir}/{name}/engine.log.
func (l *osExecLauncher) openLogFile(roleName string) io.WriteCloser {
	logDir := filepath.Join(l.logDir, roleName)
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		slog.Warn("failed to create role log dir", "dir", logDir, "error", err)
		return nil
	}

	logPath := filepath.Join(logDir, "engine.log")
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		slog.Warn("failed to open role log file", "path", logPath, "error", err)
		return nil
	}
	return f
}

// extractEnvValue finds the value of KEY=VALUE in an env slice.
func extractEnvValue(env []string, key string) string {
	prefix := key + "="
	for _, e := range env {
		if len(e) > len(prefix) && e[:len(prefix)] == prefix {
			return e[len(prefix):]
		}
	}
	return ""
}

// osProcess wraps exec.Cmd to implement the Process interface.
// Wait() uses sync.Once to prevent double-wait panics.
type osProcess struct {
	cmd      *exec.Cmd
	logFile  io.WriteCloser
	waitOnce sync.Once
	waitErr  error
}

func (p *osProcess) PID() int { return p.cmd.Process.Pid }

func (p *osProcess) Wait() error {
	p.waitOnce.Do(func() {
		p.waitErr = p.cmd.Wait()
		if p.logFile != nil {
			p.logFile.Close()
		}
	})
	return p.waitErr
}

func (p *osProcess) Terminate() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(os.Interrupt)
}

func (p *osProcess) Kill() error {
	if p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Kill()
}
