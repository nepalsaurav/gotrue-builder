package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func runPostgresCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: gotruectl postgres up|down")
	}
	switch args[0] {
	case "up":
		return postgresUp()
	case "down":
		return postgresDown()
	default:
		return fmt.Errorf("unknown postgres subcommand %q (want up|down)", args[0])
	}
}

// postgresUp is idempotent: safe to call directly or as a dependency of
// `tenant create`. It starts the shared postgres container if stopped, or
// creates it from scratch on first run.
func postgresUp() error {
	if err := dockerAvailable(); err != nil {
		return err
	}
	if err := ensureNetwork(networkName); err != nil {
		return err
	}
	if err := ensureVolume(volumeName); err != nil {
		return err
	}

	exists, running, err := containerState(postgresContainerName)
	if err != nil {
		return fmt.Errorf("checking postgres container: %w", err)
	}
	if running {
		fmt.Println("postgres already running")
		return nil
	}
	if exists {
		fmt.Println("starting existing postgres container ...")
		if err := runInherit("", "docker", "start", postgresContainerName); err != nil {
			return fmt.Errorf("starting postgres: %w", err)
		}
		return waitForPostgresReady()
	}

	password, err := readOrCreatePostgresPassword()
	if err != nil {
		return err
	}

	fmt.Println("creating postgres container ...")
	if err := runInherit("", "docker", "run", "-d",
		"--name", postgresContainerName,
		"--network", networkName,
		"--label", managedByLabel,
		"--label", "role=postgres",
		"-e", "POSTGRES_PASSWORD="+password,
		"-v", volumeName+":/var/lib/postgresql/data",
		"--restart", "unless-stopped",
		postgresImage,
	); err != nil {
		return fmt.Errorf("running postgres container: %w", err)
	}

	return waitForPostgresReady()
}

func postgresDown() error {
	exists, running, err := containerState(postgresContainerName)
	if err != nil {
		return err
	}
	if !exists || !running {
		fmt.Println("postgres not running")
		return nil
	}
	if err := runInherit("", "docker", "stop", postgresContainerName); err != nil {
		return fmt.Errorf("stopping postgres: %w", err)
	}
	fmt.Println("postgres stopped (data preserved in volume", volumeName+")")
	return nil
}

func waitForPostgresReady() error {
	fmt.Print("waiting for postgres to accept connections ")
	for i := 0; i < 30; i++ {
		if _, err := dockerExecCapture(postgresContainerName, "pg_isready", "-U", "postgres"); err == nil {
			fmt.Println(" ready")
			return nil
		}
		fmt.Print(".")
		time.Sleep(1 * time.Second)
	}
	fmt.Println()
	return fmt.Errorf("postgres did not become ready in time")
}

// readOrCreatePostgresPassword reuses the password from a prior run so
// `postgres up` stays idempotent across restarts of this CLI, generating one
// on first use.
func readOrCreatePostgresPassword() (string, error) {
	path, err := postgresEnvPath()
	if err != nil {
		return "", err
	}

	if data, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if v, ok := strings.CutPrefix(line, "POSTGRES_PASSWORD="); ok {
				return strings.TrimSpace(v), nil
			}
		}
	}

	password, err := generateSecret(24)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", err
	}
	content := "POSTGRES_PASSWORD=" + password + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return password, nil
}
