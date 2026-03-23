package roles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/config"
)

// validRoleName matches lowercase letters, numbers, and hyphens (no path traversal).
var validRoleName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// RoleInfo is a snapshot of a role's current state, returned by Status().
type RoleInfo struct {
	Name     string     `json:"name"`
	Port     int        `json:"port"`
	Status   RoleStatus `json:"status"`
	Restarts int        `json:"restarts"`
	Enabled  bool       `json:"enabled"`
}

// maxDispatchResponseBytes limits the response body size from child engines (10MB).
const maxDispatchResponseBytes = 10 << 20

// RoleManager orchestrates all child role engine processes.
type RoleManager struct {
	mu         sync.RWMutex
	processes  map[string]*RoleProcess
	launcher   ProcessLauncher
	cfg        *config.Config
	httpClient *http.Client // shared client for dispatch calls
}

// NewRoleManager creates a manager that can spawn and control child role engines.
func NewRoleManager(cfg *config.Config, launcher ProcessLauncher) *RoleManager {
	return &RoleManager{
		processes:  make(map[string]*RoleProcess),
		launcher:   launcher,
		cfg:        cfg,
		httpClient: &http.Client{Timeout: 5 * time.Minute},
	}
}

// Start spawns all roles that have auto_start enabled.
func (rm *RoleManager) Start(ctx context.Context) {
	for name, spec := range rm.cfg.Roles.List {
		if !spec.Enabled || !spec.AutoStart {
			continue
		}
		if err := rm.Spawn(ctx, name); err != nil {
			slog.Error("failed to auto-start role", "role", name, "error", err)
		}
	}
}

// Spawn starts a specific role engine process. Idempotent — returns nil if already running.
func (rm *RoleManager) Spawn(ctx context.Context, roleName string) error {
	if !validRoleName.MatchString(roleName) {
		return fmt.Errorf("invalid role name %q: must be lowercase alphanumeric with hyphens", roleName)
	}

	rm.mu.Lock()

	// Check if already running
	if rp, exists := rm.processes[roleName]; exists {
		status := rp.Status()
		if status == StatusRunning {
			rm.mu.Unlock()
			return nil // already running
		}
		// Clean up old crashed/stopped process before re-spawning
		rm.mu.Unlock()
		rp.Stop()
		rm.mu.Lock()
	}

	spec, ok := rm.cfg.Roles.List[roleName]
	if !ok {
		rm.mu.Unlock()
		return fmt.Errorf("role %q not found in config", roleName)
	}
	if !spec.Enabled {
		rm.mu.Unlock()
		return fmt.Errorf("role %q is disabled", roleName)
	}

	port := rm.portForRole(roleName)
	token := deriveChildToken(rm.cfg.Gateway.Token, roleName)

	// Generate child config file
	configPath, cleanup, err := GenerateChildConfig(rm.cfg, roleName, spec, port)
	if err != nil {
		rm.mu.Unlock()
		return fmt.Errorf("generate config for role %s: %w", roleName, err)
	}

	rp := &RoleProcess{
		Name:       roleName,
		Spec:       spec,
		Port:       port,
		Token:      token,
		ConfigPath: configPath,
		cleanup:    cleanup,
	}
	rm.processes[roleName] = rp
	rm.mu.Unlock()

	return rp.Start(ctx, rm.launcher)
}

// StopRole stops a specific role engine. Returns error if role not found.
func (rm *RoleManager) StopRole(roleName string) error {
	rm.mu.RLock()
	rp, exists := rm.processes[roleName]
	rm.mu.RUnlock()

	if !exists {
		return fmt.Errorf("role %q not running", roleName)
	}

	rp.Stop()
	return nil
}

// Stop gracefully stops all running child role engines.
func (rm *RoleManager) Stop() {
	rm.mu.RLock()
	procs := make([]*RoleProcess, 0, len(rm.processes))
	for _, rp := range rm.processes {
		procs = append(procs, rp)
	}
	rm.mu.RUnlock()

	// Stop all in parallel
	var wg sync.WaitGroup
	for _, rp := range procs {
		wg.Add(1)
		go func(rp *RoleProcess) {
			defer wg.Done()
			rp.Stop()
		}(rp)
	}
	wg.Wait()
}

// Dispatch sends a task message to a child role's /v1/chat/completions endpoint.
func (rm *RoleManager) Dispatch(ctx context.Context, roleName string, task string) (string, error) {
	rm.mu.RLock()
	rp, exists := rm.processes[roleName]
	rm.mu.RUnlock()

	if !exists {
		return "", fmt.Errorf("role %q not running", roleName)
	}
	if rp.Status() != StatusRunning {
		return "", fmt.Errorf("role %q is %s, not running", roleName, rp.Status())
	}

	// Build OpenAI-compatible request
	body := map[string]interface{}{
		"model": "default",
		"messages": []map[string]string{
			{"role": "user", "content": task},
		},
	}
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return "", fmt.Errorf("marshal dispatch request: %w", err)
	}

	url := fmt.Sprintf("http://127.0.0.1:%d/v1/chat/completions", rp.Port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(jsonBody))
	if err != nil {
		return "", fmt.Errorf("create dispatch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+rp.Token)

	resp, err := rm.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("dispatch to role %s: %w", roleName, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxDispatchResponseBytes))
	if err != nil {
		return "", fmt.Errorf("read dispatch response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("role %s returned status %d: %s", roleName, resp.StatusCode, string(respBody))
	}

	return string(respBody), nil
}

// Status returns info about all configured roles.
func (rm *RoleManager) Status() []RoleInfo {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	var infos []RoleInfo
	for name, spec := range rm.cfg.Roles.List {
		info := RoleInfo{
			Name:    name,
			Port:    rm.portForRole(name),
			Enabled: spec.Enabled,
			Status:  StatusStopped,
		}
		if rp, exists := rm.processes[name]; exists {
			info.Status = rp.Status()
			info.Restarts = rp.Restarts()
		}
		infos = append(infos, info)
	}

	// Sort by name for stable output
	sort.Slice(infos, func(i, j int) bool { return infos[i].Name < infos[j].Name })
	return infos
}

// RoleStatus returns info about a single role.
func (rm *RoleManager) RoleStatus(roleName string) (RoleInfo, bool) {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	spec, ok := rm.cfg.Roles.List[roleName]
	if !ok {
		return RoleInfo{}, false
	}

	info := RoleInfo{
		Name:    roleName,
		Port:    rm.portForRole(roleName),
		Enabled: spec.Enabled,
		Status:  StatusStopped,
	}
	if rp, exists := rm.processes[roleName]; exists {
		info.Status = rp.Status()
		info.Restarts = rp.Restarts()
	}
	return info, true
}

// portForRole returns a deterministic port for a role name.
// Formula: parentPort + 1 + sortedIndex (alphabetical order of all role names).
func (rm *RoleManager) portForRole(name string) int {
	names := make([]string, 0, len(rm.cfg.Roles.List))
	for n := range rm.cfg.Roles.List {
		names = append(names, n)
	}
	sort.Strings(names)

	for i, n := range names {
		if n == name {
			return rm.cfg.Gateway.Port + 1 + i
		}
	}
	// Fallback — should never happen if name is in config
	return rm.cfg.Gateway.Port + 1
}
