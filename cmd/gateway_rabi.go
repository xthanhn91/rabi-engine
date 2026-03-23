// gateway_rabi.go — wires Rabi-specific modules into the GoClaw gateway.
// Single entry point: initRabiModules() called from gateway.go.
// All Rabi code lives in internal/rabi/* — this file only does wiring.
package cmd

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/rabi/facebook"
	"github.com/nextlevelbuilder/goclaw/internal/rabi/hooks"
	rabihttp "github.com/nextlevelbuilder/goclaw/internal/rabi/http"
	"github.com/nextlevelbuilder/goclaw/internal/rabi/metrics"
	"github.com/nextlevelbuilder/goclaw/internal/rabi/roles"
	rabispawn "github.com/nextlevelbuilder/goclaw/internal/rabi/spawn"
	"github.com/nextlevelbuilder/goclaw/internal/tools"
)

// rabiCleanup groups deferred cleanup functions for Rabi modules.
type rabiCleanup struct {
	roleMgr *roles.RoleManager
}

func (rc *rabiCleanup) Stop() {
	if rc.roleMgr != nil {
		rc.roleMgr.Stop()
	}
}

// initRabiModules registers all Rabi-specific tools, hooks, HTTP handlers, and roles.
// Returns a cleanup function that must be deferred by the caller.
func initRabiModules(
	ctx context.Context,
	cfg *config.Config,
	toolsReg *tools.Registry,
	workspace string,
	mux *http.ServeMux,
	startupVersion string,
) *rabiCleanup {
	cleanup := &rabiCleanup{}

	// 1. CTO fleet tools — register when scripts directory exists
	scriptsDir := filepath.Join(workspace, "agents", "cto", "scripts")
	if dirExists(scriptsDir) {
		toolsReg.Register(tools.NewScanFleetTool(scriptsDir))
		toolsReg.Register(tools.NewHealthCheckTool(scriptsDir))
		toolsReg.Register(tools.NewDispatchTaskTool(scriptsDir))
		slog.Info("rabi: CTO fleet tools enabled", "scripts_dir", scriptsDir)
	}

	// 2. Facebook tool — opt-in via config
	if cfg.Facebook.Enabled {
		sidecarURL := cfg.Facebook.SidecarURL
		if sidecarURL == "" {
			sidecarURL = "http://localhost:7788"
		}
		fbTool := facebook.NewFacebookTool(
			sidecarURL,
			cfg.Facebook.AccountID,
			cfg.Facebook.Email,
			cfg.Facebook.Password,
		)
		if fbTool.Client().HealthCheck(ctx) {
			slog.Info("rabi: facebook sidecar connected", "url", sidecarURL)
		} else {
			slog.Warn("rabi: facebook sidecar unreachable (will retry on use)", "url", sidecarURL)
		}
		toolsReg.Register(fbTool)
		slog.Info("rabi: facebook_action tool registered")
	}

	// 3. Loop hooks validation (hooks are wired into agent loop separately)
	if len(cfg.Agents.Defaults.Hooks) > 0 {
		if err := hooks.ValidateLoopHooks(cfg.Agents.Defaults.Hooks); err != nil {
			slog.Error("rabi: invalid loop hooks config", "error", err)
		} else {
			slog.Info("rabi: loop hooks configured", "count", len(cfg.Agents.Defaults.Hooks))
		}
	}

	// 4. Multi-role engine — spawn child processes per role
	var roleMgr *roles.RoleManager
	if cfg.Roles.Enabled && len(cfg.Roles.List) > 0 {
		homeDir, _ := os.UserHomeDir()
		logDir := filepath.Join(homeDir, ".goclaw", "rabi-roles")
		launcher := roles.NewOSExecLauncher(logDir)
		roleMgr = roles.NewRoleManager(cfg, launcher)
		roleMgr.Start(ctx)
		cleanup.roleMgr = roleMgr
		slog.Info("rabi: role manager started", "roles", len(cfg.Roles.List))
	}

	// 5. Metrics endpoints (Jarvis dashboard)
	metricsCollector := metrics.NewMetricsCollector(
		time.Now(), roleMgr, toolsReg, cfg, startupVersion, workspace,
	)
	metricsHandler := metrics.NewMetricsHandler(metricsCollector, cfg.Gateway.Token)
	metricsHandler.RegisterRoutes(mux)
	slog.Info("rabi: metrics endpoints registered")

	// 6. Roles HTTP handler
	if roleMgr != nil {
		rolesHandler := rabihttp.NewRolesHandler(roleMgr, cfg.Gateway.Token)
		rolesHandler.RegisterRoutes(mux)
		slog.Info("rabi: roles HTTP API registered")
	}

	// 7. Zalo tools — registered from channel config in registerConfigChannels
	// (ZaloSendTool/ZaloReconnectTool need the personal channel instance, wired there)

	// 8. Role-aware spawn wrapper — injects role context (SOUL.md, AGENTS.md) into subagent tasks.
	// Convention: roles/{type}/SOUL.md + roles/{type}/AGENTS.md at workspace root.
	// Wraps the standard "spawn" tool without modifying GoClaw core.
	rolesDir := filepath.Join(workspace, "roles")
	if dirExists(rolesDir) {
		if spawnTool, ok := toolsReg.Get("spawn"); ok && spawnTool != nil {
			toolsReg.Register(rabispawn.NewRoleSpawnTool(spawnTool, workspace))
			slog.Info("rabi: role-aware spawn wrapper registered", "roles_dir", rolesDir)
		}
	}

	return cleanup
}

// dirExists returns true if the path is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
