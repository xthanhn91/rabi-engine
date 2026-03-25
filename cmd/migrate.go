package cmd

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/spf13/cobra"

	"github.com/nextlevelbuilder/goclaw/internal/config"
	"github.com/nextlevelbuilder/goclaw/internal/upgrade"
)

// EmbeddedMigrationsFS holds the embedded migrations/*.sql files.
// Set by main() before Execute(). When set, newMigrator() uses this
// as a fallback when the filesystem migrations directory is not found.
var EmbeddedMigrationsFS embed.FS

var migrationsDir string

func resolveMigrationsDir() string {
	if migrationsDir != "" {
		return migrationsDir
	}
	// Allow env override (used by Docker entrypoint).
	if v := os.Getenv("GOCLAW_MIGRATIONS_DIR"); v != "" {
		return v
	}
	// Default: ./migrations relative to the executable's working directory.
	exe, err := os.Executable()
	if err != nil {
		return "migrations"
	}
	return filepath.Join(filepath.Dir(exe), "migrations")
}

func newMigrator(dsn string) (*migrate.Migrate, error) {
	// Prefer filesystem directory (dev mode / explicit override).
	dir := resolveMigrationsDir()
	if _, err := os.Stat(dir); err == nil {
		m, err := migrate.New("file://"+dir, dsn)
		if err != nil {
			return nil, fmt.Errorf("create migrator (filesystem): %w", err)
		}
		slog.Debug("using filesystem migrations", "dir", dir)
		return m, nil
	}

	// Fallback: use migrations embedded in the binary.
	// EmbeddedMigrationsFS is set by main() from //go:embed migrations/*.sql.
	var zero embed.FS
	if EmbeddedMigrationsFS != zero {
		// The embedded FS has files at "migrations/*.sql", so we sub into that dir.
		subFS, err := fs.Sub(EmbeddedMigrationsFS, "migrations")
		if err != nil {
			return nil, fmt.Errorf("sub embedded migrations FS: %w", err)
		}
		source, err := iofs.New(subFS, ".")
		if err != nil {
			return nil, fmt.Errorf("create iofs source: %w", err)
		}
		m, err := migrate.NewWithSourceInstance("iofs", source, dsn)
		if err != nil {
			return nil, fmt.Errorf("create migrator (embedded): %w", err)
		}
		slog.Debug("using embedded migrations")
		return m, nil
	}

	// Neither filesystem nor embedded available — return helpful error.
	return nil, fmt.Errorf("migrations directory %q not found and no embedded migrations available", dir)
}

func resolveDSN() (string, error) {
	// DSN comes from environment only (secret, never in config.json).
	// config.Load also reads GOCLAW_POSTGRES_DSN into cfg.Database.PostgresDSN.
	cfg, err := config.Load(resolveConfigPath())
	if err != nil {
		return "", fmt.Errorf("load config: %w", err)
	}
	dsn := cfg.Database.PostgresDSN
	if dsn == "" {
		return "", fmt.Errorf("GOCLAW_POSTGRES_DSN environment variable is not set")
	}
	return dsn, nil
}

func migrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Database migration management",
	}

	cmd.PersistentFlags().StringVar(&migrationsDir, "migrations-dir", "", "path to migrations directory (default: ./migrations)")

	cmd.AddCommand(migrateUpCmd())
	cmd.AddCommand(migrateDownCmd())
	cmd.AddCommand(migrateVersionCmd())
	cmd.AddCommand(migrateForceCmd())
	cmd.AddCommand(migrateGotoCmd())
	cmd.AddCommand(migrateDropCmd())

	return cmd
}

func migrateUpCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Apply all pending migrations",
		RunE: func(cmd *cobra.Command, args []string) error {
			dsn, err := resolveDSN()
			if err != nil {
				return err
			}
			m, err := newMigrator(dsn)
			if err != nil {
				return err
			}
			defer m.Close()

			if err := m.Up(); err != nil && !errors.Is(err, migrate.ErrNoChange) {
				return fmt.Errorf("migrate up: %w", err)
			}

			v, dirty, _ := m.Version()
			slog.Info("migration complete", "version", v, "dirty", dirty)

			// Run pending data hooks after SQL migrations.
			db, dbErr := sql.Open("pgx", dsn)
			if dbErr != nil {
				slog.Warn("could not connect for data hooks", "error", dbErr)
				return nil
			}
			defer db.Close()

			count, hookErr := upgrade.RunPendingHooks(context.Background(), db)
			if hookErr != nil {
				slog.Warn("data hooks failed", "error", hookErr)
			} else if count > 0 {
				slog.Info("data hooks applied", "count", count)
			}

			return nil
		},
	}
}

func migrateDownCmd() *cobra.Command {
	var steps int
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Roll back migrations (default: 1 step)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dsn, err := resolveDSN()
			if err != nil {
				return err
			}
			m, err := newMigrator(dsn)
			if err != nil {
				return err
			}
			defer m.Close()

			if steps <= 0 {
				steps = 1
			}
			if err := m.Steps(-steps); err != nil && !errors.Is(err, migrate.ErrNoChange) {
				return fmt.Errorf("migrate down: %w", err)
			}

			v, dirty, _ := m.Version()
			slog.Info("rollback complete", "version", v, "dirty", dirty)
			return nil
		},
	}
	cmd.Flags().IntVarP(&steps, "steps", "n", 1, "number of steps to roll back")
	return cmd
}

func migrateVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Show current migration version",
		RunE: func(cmd *cobra.Command, args []string) error {
			dsn, err := resolveDSN()
			if err != nil {
				return err
			}
			m, err := newMigrator(dsn)
			if err != nil {
				return err
			}
			defer m.Close()

			v, dirty, err := m.Version()
			if err != nil {
				return fmt.Errorf("get version: %w", err)
			}
			fmt.Printf("version: %d, dirty: %v\n", v, dirty)
			return nil
		},
	}
}

func migrateForceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "force <version>",
		Short: "Force set migration version (no migration applied)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			version, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("invalid version: %w", err)
			}
			dsn, err := resolveDSN()
			if err != nil {
				return err
			}
			m, err := newMigrator(dsn)
			if err != nil {
				return err
			}
			defer m.Close()

			if err := m.Force(version); err != nil {
				return fmt.Errorf("force version: %w", err)
			}
			slog.Info("forced version", "version", version)
			return nil
		},
	}
}

func migrateGotoCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "goto <version>",
		Short: "Migrate to a specific version",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			version, err := strconv.ParseUint(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid version: %w", err)
			}
			dsn, err := resolveDSN()
			if err != nil {
				return err
			}
			m, err := newMigrator(dsn)
			if err != nil {
				return err
			}
			defer m.Close()

			if err := m.Migrate(uint(version)); err != nil && !errors.Is(err, migrate.ErrNoChange) {
				return fmt.Errorf("migrate goto: %w", err)
			}
			slog.Info("migrated to version", "version", version)
			return nil
		},
	}
}

func migrateDropCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "drop",
		Short: "Drop all tables (DANGEROUS)",
		RunE: func(cmd *cobra.Command, args []string) error {
			dsn, err := resolveDSN()
			if err != nil {
				return err
			}
			m, err := newMigrator(dsn)
			if err != nil {
				return err
			}
			defer m.Close()

			if err := m.Drop(); err != nil {
				return fmt.Errorf("drop: %w", err)
			}
			slog.Info("all tables dropped")
			return nil
		},
	}
}
