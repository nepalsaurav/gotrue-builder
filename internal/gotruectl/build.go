package gotruectl

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

// Phase-2 path: build supabase/auth from source instead of pulling the
// official image. Not used by `postgres`/`tenant` (which pull
// supabase/auth:v2.196.0 directly) — kept for when a patched GoTrue or an
// offline build is needed.
const (
	defaultVersion = "v2.196.0"
	defaultDest    = "/opt/gotrue"
	repoURL        = "https://github.com/supabase/auth.git"
	binName        = "gotrue-auth"
)

func newBuildCmd() *cobra.Command {
	var version, dest, workdir string
	cmd := &cobra.Command{
		Use:   "build",
		Short: "Clone and build supabase/auth from source (phase-2 self-built-image path)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return build(version, workdir, dest)
		},
	}
	cmd.Flags().StringVar(&version, "version", defaultVersion, "supabase/auth tag to build")
	cmd.Flags().StringVar(&dest, "dest", defaultDest, "directory to copy the built binary into")
	cmd.Flags().StringVar(&workdir, "workdir", ".gotrue-builder-src", "directory to clone supabase/auth source into")
	return cmd
}

func build(version, workdir, dest string) error {
	srcDir := filepath.Join(workdir, version)

	if _, err := os.Stat(filepath.Join(srcDir, ".git")); err != nil {
		fmt.Printf("cloning supabase/auth %s ...\n", version)
		if err := runInherit("", "git", "clone", "--branch", version, "--depth", "1", repoURL, srcDir); err != nil {
			return fmt.Errorf("clone: %w", err)
		}
	} else {
		fmt.Println("source already cloned, reusing", srcDir)
	}

	commit, err := runCapture(srcDir, "git", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("git rev-parse: %w", err)
	}

	fmt.Println("building ...")
	builtPath := filepath.Join(srcDir, binName)
	ldflags := fmt.Sprintf("-X github.com/supabase/auth/cmd.Version=%s", commit)
	if err := runInherit(srcDir, "go", "build", "-ldflags", ldflags, "-o", builtPath, "."); err != nil {
		return fmt.Errorf("go build: %w", err)
	}

	if err := os.MkdirAll(dest, 0o755); err != nil {
		return fmt.Errorf("creating %s: %w", dest, err)
	}
	destPath := filepath.Join(dest, binName)
	if err := copyFile(builtPath, destPath, 0o755); err != nil {
		return fmt.Errorf("copying binary to %s: %w", destPath, err)
	}

	fmt.Printf("built %s (%s) -> %s\n", version, commit, destPath)
	return nil
}
