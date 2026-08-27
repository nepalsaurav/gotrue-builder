// Package gotruectl implements the gotruectl CLI: a Docker-based manager
// for a shared Postgres container plus one GoTrue container per tenant,
// plus cross-cutting status/backup/update/admin-API tooling around it.
package gotruectl

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// Version is set at build time via -ldflags "-X .../internal/gotruectl.Version=...".
var Version = "dev"

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "gotruectl",
		Short:         "Manage a local Docker-based GoTrue setup: shared Postgres + per-tenant GoTrue containers",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default ~/.gotrue-builder/config.yaml)")

	cmd.AddCommand(newPostgresCmd())
	cmd.AddCommand(newTenantCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newBackupCmd())
	cmd.AddCommand(newUpdateCmd())
	cmd.AddCommand(newKeyCmd())
	cmd.AddCommand(newAdminCmd())
	cmd.AddCommand(newConfigCmd())
	cmd.AddCommand(newBuildCmd())
	cmd.AddCommand(newSelfUpdateCmd())
	return cmd
}

// Execute runs the CLI; cmd/gotruectl's main() just calls this.
func Execute() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
