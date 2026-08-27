package gotruectl

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"
)

func newPostgresCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "postgres",
		Short: "Manage the shared Postgres container every tenant's database lives in",
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "up",
		Short: "Start the shared postgres container (idempotent)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			return postgresUp(cfg)
		},
	})
	cmd.AddCommand(&cobra.Command{
		Use:   "down",
		Short: "Stop the shared postgres container (data stays in the volume)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			return postgresDown(cfg)
		},
	})
	return cmd
}

// postgresUp is idempotent: safe to call directly or as a dependency of
// `tenant create`. It starts the shared postgres container if stopped, or
// creates it from scratch on first run.
func postgresUp(cfg *Config) error {
	if err := dockerAvailable(); err != nil {
		return err
	}
	if err := ensureNetwork(cfg.Network); err != nil {
		return err
	}
	if err := ensureVolume(cfg.Volume); err != nil {
		return err
	}

	exists, running, err := containerState(postgresContainerName)
	if err != nil {
		return fmt.Errorf("checking postgres container: %w", err)
	}
	if running {
		printMuted("postgres already running")
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
	// POSTGRES_PASSWORD is passed as a bare "-e POSTGRES_PASSWORD" (no
	// value) plus the value in this *process's own* environment — never as
	// "-e POSTGRES_PASSWORD=<value>", which would put the plaintext
	// password in argv, visible to any local user via `ps aux` for as long
	// as this docker run invocation is executing.
	if err := runInheritWithSecretEnv("POSTGRES_PASSWORD", password, "docker", "run", "-d",
		"--name", postgresContainerName,
		"--network", cfg.Network,
		"--label", managedByLabel,
		"--label", "role=postgres",
		"-e", "POSTGRES_PASSWORD",
		"-v", cfg.Volume+":/var/lib/postgresql/data",
		"--restart", "unless-stopped",
		cfg.PostgresImage,
	); err != nil {
		return fmt.Errorf("running postgres container: %w", err)
	}

	return waitForPostgresReady()
}

func postgresDown(cfg *Config) error {
	exists, running, err := containerState(postgresContainerName)
	if err != nil {
		return err
	}
	if !exists || !running {
		printMuted("postgres not running")
		return nil
	}
	if err := runInherit("", "docker", "stop", postgresContainerName); err != nil {
		return fmt.Errorf("stopping postgres: %w", err)
	}
	printSuccess("postgres stopped (data preserved in volume %s)", cfg.Volume)
	return nil
}

func waitForPostgresReady() error {
	fmt.Print("waiting for postgres to accept connections ")
	for i := 0; i < 30; i++ {
		if _, err := dockerExecCapture(postgresContainerName, "pg_isready", "-U", "postgres"); err == nil {
			fmt.Println()
			printSuccess("postgres ready")
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

	if env, err := parseEnvFile(path); err == nil {
		if v, ok := env["POSTGRES_PASSWORD"]; ok {
			return v, nil
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
