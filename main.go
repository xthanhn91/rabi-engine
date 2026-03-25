package main

import (
	_ "time/tzdata" // embed IANA timezone database for containers without tzdata

	"github.com/nextlevelbuilder/goclaw/cmd"
)

func main() {
	// Pass embedded assets to the cmd package before executing commands.
	// Both FS vars are defined in embed.go via //go:embed directives.
	cmd.EmbeddedMigrationsFS = migrationsFS
	cmd.EmbeddedDashboardFS = dashboardFS
	cmd.Execute()
}
