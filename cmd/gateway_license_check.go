package cmd

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/nextlevelbuilder/goclaw/internal/license"
)

// checkLicense validates the license key on gateway startup.
// Returns the cancel function for the phone-home goroutine (nil if skipped).
//
// License is OPTIONAL in development: set GOCLAW_LICENSE_KEY="" or
// GOCLAW_SKIP_LICENSE=1 to skip the check entirely.
func checkLicense(ctx context.Context) context.CancelFunc {
	// Skip license check in dev mode
	if os.Getenv("GOCLAW_SKIP_LICENSE") == "1" {
		slog.Info("license check skipped (GOCLAW_SKIP_LICENSE=1)")
		return func() {}
	}

	key := os.Getenv("GOCLAW_LICENSE_KEY")
	if key == "" {
		// No key configured — license system not active yet.
		// This allows the open-source / self-hosted mode to work without a key.
		slog.Debug("no GOCLAW_LICENSE_KEY set, running without license validation")
		return func() {}
	}

	serverURL := os.Getenv("GOCLAW_LICENSE_SERVER")
	stateDir := filepath.Dir(resolveConfigPath())
	validator := license.NewValidator(serverURL, stateDir)

	resp, err := validator.Validate(key)
	if err != nil {
		slog.Error("license validation failed", "error", err)
		slog.Error("set GOCLAW_SKIP_LICENSE=1 to bypass (development only)")
		os.Exit(1)
	}

	slog.Info("license validated",
		"plan", resp.Plan,
		"max_agents", resp.MaxAgents,
		"max_tenants", resp.MaxTenants,
	)

	// Start background phone-home (validates every 24h)
	cancelPhoneHome := license.PhoneHome(ctx, validator, key)
	return cancelPhoneHome
}
