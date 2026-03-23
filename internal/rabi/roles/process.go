package roles

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/config"
)

// RoleStatus represents the current state of a role process.
type RoleStatus string

const (
	StatusStarting   RoleStatus = "starting"
	StatusRunning    RoleStatus = "running"
	StatusStopped    RoleStatus = "stopped"
	StatusCrashed    RoleStatus = "crashed"
	StatusRestarting RoleStatus = "restarting"
)

const (
	healthTimeout      = 30 * time.Second // max time to wait for child to become healthy
	healthInterval     = 1 * time.Second  // polling interval for health checks
	stopTimeout        = 5 * time.Second  // time to wait for graceful shutdown before SIGKILL
	maxBackoff         = 5 * time.Minute  // maximum restart backoff duration
	baseBackoff        = 30 * time.Second // initial restart backoff duration
	defaultMaxRestarts = 3
)

// RoleProcess manages a single child engine process lifecycle.
type RoleProcess struct {
	Name       string
	Spec       config.RoleSpec
	Port       int
	Token      string
	ConfigPath string

	mu       sync.RWMutex
	proc     Process
	status   RoleStatus
	restarts int
	cleanup  func()             // removes temp config file
	cancelWd context.CancelFunc // cancels watchdog goroutine
}

// Status returns the current status of the role process.
func (rp *RoleProcess) Status() RoleStatus {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	return rp.status
}

// Restarts returns the number of times this role has been restarted.
func (rp *RoleProcess) Restarts() int {
	rp.mu.RLock()
	defer rp.mu.RUnlock()
	return rp.restarts
}

// Start launches the child engine process and waits for it to become healthy.
func (rp *RoleProcess) Start(ctx context.Context, launcher ProcessLauncher) error {
	rp.mu.Lock()
	rp.status = StatusStarting
	rp.mu.Unlock()

	args := []string{"--config", rp.ConfigPath}
	env := []string{
		fmt.Sprintf("GOCLAW_CONFIG=%s", rp.ConfigPath),
		fmt.Sprintf("GOCLAW_ROLE_NAME=%s", rp.Name),
	}

	proc, err := launcher.Launch(ctx, args, env)
	if err != nil {
		rp.mu.Lock()
		rp.status = StatusCrashed
		rp.mu.Unlock()
		return fmt.Errorf("launch role %s: %w", rp.Name, err)
	}

	rp.mu.Lock()
	rp.proc = proc
	rp.mu.Unlock()

	// Wait for the child's /health endpoint to respond
	if err := rp.waitForHealth(ctx); err != nil {
		// Health check failed — terminate the process
		proc.Terminate()
		proc.Wait()
		rp.mu.Lock()
		rp.status = StatusCrashed
		rp.mu.Unlock()
		return fmt.Errorf("health check failed for role %s: %w", rp.Name, err)
	}

	rp.mu.Lock()
	rp.status = StatusRunning
	rp.mu.Unlock()

	// Start watchdog goroutine for automatic restart
	wdCtx, wdCancel := context.WithCancel(ctx)
	rp.mu.Lock()
	rp.cancelWd = wdCancel
	rp.mu.Unlock()
	go rp.watchdog(wdCtx, launcher)

	slog.Info("role started", "role", rp.Name, "pid", proc.PID(), "port", rp.Port)
	return nil
}

// Stop gracefully terminates the child engine process.
func (rp *RoleProcess) Stop() {
	rp.mu.Lock()
	proc := rp.proc
	cancelWd := rp.cancelWd
	rp.mu.Unlock()

	// Cancel watchdog first to prevent restart after stop
	if cancelWd != nil {
		cancelWd()
	}

	if proc == nil {
		return
	}

	// Send SIGTERM for graceful shutdown
	if err := proc.Terminate(); err != nil {
		slog.Warn("failed to terminate role", "role", rp.Name, "error", err)
	}

	// Wait for process to exit with timeout
	done := make(chan struct{})
	go func() {
		proc.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Process exited gracefully
	case <-time.After(stopTimeout):
		slog.Warn("force killing role process", "role", rp.Name)
		proc.Kill()
		proc.Wait()
	}

	rp.mu.Lock()
	rp.status = StatusStopped
	rp.proc = nil
	rp.mu.Unlock()

	// Clean up temp config file
	if rp.cleanup != nil {
		rp.cleanup()
	}

	slog.Info("role stopped", "role", rp.Name)
}

// waitForHealth polls the child's /health endpoint until it responds or times out.
func (rp *RoleProcess) waitForHealth(ctx context.Context) error {
	healthURL := fmt.Sprintf("http://127.0.0.1:%d/health", rp.Port)
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(healthTimeout)

	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		resp, err := client.Get(healthURL)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(healthInterval)
	}
	return fmt.Errorf("timed out after %s", healthTimeout)
}

// watchdog monitors the child process and restarts it on crash with exponential backoff.
func (rp *RoleProcess) watchdog(ctx context.Context, launcher ProcessLauncher) {
	rp.mu.RLock()
	proc := rp.proc
	rp.mu.RUnlock()

	if proc == nil {
		return
	}

	// Wait for process to exit
	waitErr := proc.Wait()

	// Check if we were intentionally stopped
	select {
	case <-ctx.Done():
		return // intentional stop, don't restart
	default:
	}

	maxRestarts := rp.Spec.MaxRestarts
	if maxRestarts <= 0 {
		maxRestarts = defaultMaxRestarts
	}

	rp.mu.Lock()
	rp.restarts++
	restarts := rp.restarts
	rp.status = StatusCrashed
	rp.mu.Unlock()

	slog.Warn("role process exited unexpectedly",
		"role", rp.Name, "error", waitErr, "restarts", restarts)

	if restarts > maxRestarts {
		slog.Error("role exceeded max restarts, giving up",
			"role", rp.Name, "max", maxRestarts)
		return
	}

	// Exponential backoff: baseBackoff * 2^(restarts-1), capped at maxBackoff
	backoff := baseBackoff
	for i := 1; i < restarts; i++ {
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
			break
		}
	}

	rp.mu.Lock()
	rp.status = StatusRestarting
	rp.mu.Unlock()

	slog.Info("restarting role after backoff",
		"role", rp.Name, "backoff", backoff, "attempt", restarts)

	select {
	case <-ctx.Done():
		return
	case <-time.After(backoff):
	}

	if err := rp.Start(ctx, launcher); err != nil {
		slog.Error("failed to restart role", "role", rp.Name, "error", err)
	}
}
