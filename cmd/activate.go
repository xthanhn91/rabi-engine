package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/nextlevelbuilder/goclaw/internal/license"
)

func activateCmd() *cobra.Command {
	var key string
	var serverURL string

	cmd := &cobra.Command{
		Use:   "activate",
		Short: "Activate a GoClaw license key on this machine",
		Long: `Registers your license key with the GoClaw license server and
binds it to this machine's hardware fingerprint. The key is saved
locally so the gateway can validate on startup.

Example:
  ./goclaw activate --key GOCLAW-XXXX-XXXX-XXXX`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// License key can come from flag, env, or positional arg
			if key == "" {
				key = os.Getenv("GOCLAW_LICENSE_KEY")
			}
			if key == "" && len(args) > 0 {
				key = args[0]
			}
			if key == "" {
				return fmt.Errorf("license key required: use --key flag or GOCLAW_LICENSE_KEY env var")
			}

			// Resolve state directory (same dir as config file)
			stateDir := filepath.Dir(resolveConfigPath())

			fmt.Printf("Activating license on this machine...\n")
			fmt.Printf("  Machine ID: %s\n", license.MachineID())

			validator := license.NewValidator(serverURL, stateDir)
			resp, err := validator.Validate(key)
			if err != nil {
				return fmt.Errorf("activation failed: %w", err)
			}

			fmt.Printf("\n✓ License activated successfully!\n")
			fmt.Printf("  Plan:        %s\n", resp.Plan)
			fmt.Printf("  Max agents:  %d\n", resp.MaxAgents)
			fmt.Printf("  Max tenants: %d\n", resp.MaxTenants)
			if resp.ExpiresAt != nil {
				fmt.Printf("  Expires:     %s\n", *resp.ExpiresAt)
			} else {
				fmt.Printf("  Expires:     never\n")
			}
			fmt.Printf("\nLicense state saved. The gateway will validate automatically on startup.\n")

			return nil
		},
	}

	cmd.Flags().StringVar(&key, "key", "", "license key (GOCLAW-XXXX-XXXX-XXXX)")
	cmd.Flags().StringVar(&serverURL, "server", "", "license server URL (default: production)")

	return cmd
}
