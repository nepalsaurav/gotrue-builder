// Package gotruectl implements the gotruectl CLI: a Docker-based manager
// for a shared Postgres container plus one GoTrue container per tenant,
// plus cross-cutting status/backup/update/admin-API tooling around it.
package gotruectl

import (
	"fmt"
	"os"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version reads the module version Go's own toolchain embeds into the
// binary at `go install module@version` time (any version — a tag or
// @latest) — no -ldflags injection needed, and it stays correct regardless
// of how the binary was installed.
func version() string {
	info, ok := debug.ReadBuildInfo()
	if !ok || info.Main.Version == "" {
		return "dev"
	}
	return info.Main.Version
}

func newRootCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "gotruectl",
		Short:         "Manage a local Docker-based GoTrue setup: shared Postgres + per-tenant GoTrue containers",
		Version:       version(),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	cmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default ~/.gotrue-builder/config.yaml)")

	cmd.AddCommand(newPostgresCmd())
	cmd.AddCommand(newTenantCmd())
	cmd.AddCommand(newStatusCmd())
	cmd.AddCommand(newDoctorCmd())
	cmd.AddCommand(newDashboardCmd())
	cmd.AddCommand(newCaddyfileCmd())
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
		fmt.Fprintln(os.Stderr, errorStyle.Render("error:"), err)
		os.Exit(1)
	}
}
