package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

// runInherit runs a command with stdio connected to the current process —
// used for anything the user should watch live (docker run, git clone, go build).
func runInherit(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

// runCapture runs a command and returns its trimmed stdout, with stderr
// surfaced in the error on failure.
func runCapture(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if stderr.Len() > 0 {
			return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
		}
		return "", fmt.Errorf("%s %s: %w", name, strings.Join(args, " "), err)
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

// generateSecret returns a random hex string suitable for JWT secrets and
// generated passwords — n is the number of random bytes (hex doubles it).
func generateSecret(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating random secret: %w", err)
	}
	return hex.EncodeToString(b), nil
}
