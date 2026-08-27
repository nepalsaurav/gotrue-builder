package gotruectl

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
)

// modulePath is the install path for this binary specifically (not the
// module root) — go install always needs the exact package path.
const modulePath = "github.com/nepalsaurav/gotrue-builder/cmd/gotruectl"

func newSelfUpdateCmd() *cobra.Command {
	var version string
	cmd := &cobra.Command{
		Use:   "self-update",
		Short: "Update gotruectl itself via `go install`",
		Long: fmt.Sprintf(`Re-runs "go install %s@<version>" (default @latest) to fetch and install
the newest gotruectl. Requires a Go toolchain on this machine — that's how
this tool is distributed (no separate release binaries).`, modulePath),
		RunE: func(cmd *cobra.Command, args []string) error {
			return selfUpdate(version)
		},
	}
	cmd.Flags().StringVar(&version, "version", "latest", "module version/tag to install")
	return cmd
}

func selfUpdate(version string) error {
	if _, err := exec.LookPath("go"); err != nil {
		return fmt.Errorf("go toolchain not found on PATH — install Go first: %w", err)
	}
	target := fmt.Sprintf("%s@%s", modulePath, version)
	fmt.Println("go install", target, "...")
	if err := runInherit("", "go", "install", target); err != nil {
		return fmt.Errorf("go install %s: %w", target, err)
	}
	fmt.Println("done — the updated binary is at $(go env GOPATH)/bin/gotruectl (make sure that's on your PATH)")
	return nil
}
