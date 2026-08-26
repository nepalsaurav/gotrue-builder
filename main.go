// Command gotrue-builder clones a pinned tag of supabase/auth (GoTrue),
// builds it, and copies the resulting binary into place — e.g. on a VM
// running `gotrue-builder build` directly, no CI involved.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const (
	defaultVersion = "v2.196.0"
	defaultDest    = "/opt/gotrue"
	repoURL        = "https://github.com/supabase/auth.git"
	binName        = "gotrue-auth"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "build" {
		fmt.Fprintln(os.Stderr, "usage: gotrue-builder build [-version v2.196.0] [-dest /opt/gotrue]")
		os.Exit(1)
	}

	fs := flag.NewFlagSet("build", flag.ExitOnError)
	version := fs.String("version", defaultVersion, "supabase/auth tag to build")
	dest := fs.String("dest", defaultDest, "directory to copy the built binary into")
	workdir := fs.String("workdir", ".gotrue-builder-src", "directory to clone supabase/auth source into")
	fs.Parse(os.Args[2:])

	if err := build(*version, *workdir, *dest); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func build(version, workdir, dest string) error {
	srcDir := filepath.Join(workdir, version)

	if _, err := os.Stat(filepath.Join(srcDir, ".git")); err != nil {
		fmt.Printf("cloning supabase/auth %s ...\n", version)
		if err := run("", "git", "clone", "--branch", version, "--depth", "1", repoURL, srcDir); err != nil {
			return fmt.Errorf("clone: %w", err)
		}
	} else {
		fmt.Println("source already cloned, reusing", srcDir)
	}

	commit, err := output(srcDir, "git", "rev-parse", "HEAD")
	if err != nil {
		return fmt.Errorf("git rev-parse: %w", err)
	}

	fmt.Println("building ...")
	builtPath := filepath.Join(srcDir, binName)
	ldflags := fmt.Sprintf("-X github.com/supabase/auth/cmd.Version=%s", commit)
	if err := run(srcDir, "go", "build", "-ldflags", ldflags, "-o", builtPath, "."); err != nil {
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

func run(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func output(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func copyFile(src, dst string, perm os.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, perm)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}
	return out.Chmod(perm)
}
