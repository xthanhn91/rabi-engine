package main

import "embed"

// migrationsFS embeds all SQL migration files into the binary.
// This allows the binary to run migrations without needing the
// migrations/ directory on disk. The cmd package reads this via
// cmd.EmbeddedMigrationsFS which is set in main() before Execute().
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// dashboardFS embeds the pre-built React SPA (ui/web/dist/).
// The "all:" prefix includes dotfiles (e.g. .vite/manifest).
// When the dist/ directory doesn't exist at build time (e.g. dev mode
// without running pnpm build), the binary still compiles — it just
// won't serve the dashboard and will show a helpful message instead.
//
//go:embed all:ui/web/dist
var dashboardFS embed.FS
