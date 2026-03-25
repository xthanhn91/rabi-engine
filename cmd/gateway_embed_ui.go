package cmd

import (
	"embed"
	"io/fs"
	"log/slog"

	"github.com/nextlevelbuilder/goclaw/internal/gateway"
)

// EmbeddedDashboardFS holds the pre-built React SPA (ui/web/dist/).
// Set by main() before Execute(). When set, the gateway serves the
// dashboard from the binary itself — no separate web server needed.
var EmbeddedDashboardFS embed.FS

// setupEmbeddedDashboard extracts the dist/ subtree from the embedded
// FS and registers it with the gateway server as the SPA handler.
func setupEmbeddedDashboard(server *gateway.Server) {
	var zero embed.FS
	if EmbeddedDashboardFS == zero {
		slog.Info("no embedded dashboard UI — build with 'make build-dist' to include")
		return
	}

	// The embedded FS has files at "ui/web/dist/..." — sub into that prefix
	// so the handler sees index.html at the root.
	distFS, err := fs.Sub(EmbeddedDashboardFS, "ui/web/dist")
	if err != nil {
		slog.Error("failed to access embedded dashboard", "error", err)
		return
	}

	// Verify index.html exists (catches build issues early).
	if _, err := fs.Stat(distFS, "index.html"); err != nil {
		slog.Warn("embedded dashboard missing index.html — UI will not be served", "error", err)
		return
	}

	server.SetDashboardFS(distFS)
	slog.Info("embedded dashboard UI configured")
}
