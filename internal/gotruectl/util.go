package gotruectl

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

// runInheritWithSecretEnv is runInherit plus one extra environment variable
// set only in the child process's environment, never as a command-line
// argument. Use this instead of passing "-e KEY=value" to `docker run` for
// anything secret: an argv value is visible to any local user via `ps aux`
// or /proc/<pid>/cmdline for the process's whole lifetime, while a value
// set through cmd.Env only reaches the child's own environment (readable
// via /proc/<pid>/environ, which most systems restrict to the same user or
// root). Call with a bare "-e KEY" (no "=value") in args — Docker then
// takes the value from its own environment, which is exactly this.
func runInheritWithSecretEnv(secretKey, secretValue, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(os.Environ(), secretKey+"="+secretValue)
	return cmd.Run()
}

// runCaptureStdin is runCapture, but feeds stdinData to the command's
// stdin instead of passing secret values as command-line arguments —
// used for `psql -c` invocations that would otherwise put a password in
// argv (see runInheritWithSecretEnv for why that matters).
func runCaptureStdin(stdinData, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdinData)
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

// runInheritStdin is runInherit, but feeds stdinData to the command's
// stdin instead of passing secret values as command-line arguments.
func runInheritStdin(stdinData, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdinData)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
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

// upsertEnvValue rewrites the value of a KEY=... line in a godotenv-style
// file's content, or appends a new KEY=value line if it wasn't present —
// used by anything that edits a tenant's .env in place (JWT rotation,
// `tenant config set`), since not every tenant's file has every optional
// key (e.g. one created before SMTP was configured has no GOTRUE_SMTP_*
// lines at all).
func upsertEnvValue(content, key, newValue string) string {
	lines := strings.Split(content, "\n")
	line := key + "=" + newValue
	for i, l := range lines {
		if strings.HasPrefix(l, key+"=") {
			lines[i] = line
			return strings.Join(lines, "\n")
		}
	}
	if len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = append(lines[:len(lines)-1], line, "")
	} else {
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

// parseEnvFile reads a godotenv-style KEY=value file into a map, skipping
// blank lines.
func parseEnvFile(path string) (map[string]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	env := map[string]string{}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			env[k] = v
		}
	}
	return env, nil
}
