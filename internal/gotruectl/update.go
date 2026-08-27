package gotruectl

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Safely update a tenant's GoTrue version or rotate its JWT secret",
		Long: `Docker only lets one container bind a host port at a time, so a truly
zero-downtime swap isn't possible without a reverse proxy in front (out of
scope here). Instead both subcommands do a safe blue/green swap: pull/verify
first, swap the container, health-check the result, and automatically roll
back to the exact previous container if the new one doesn't come up healthy.
Expect a few seconds of downtime during the swap itself — never a missing
or broken container.`,
	}
	cmd.AddCommand(newUpdateRunCmd())
	cmd.AddCommand(newUpdateRotateJWTCmd())
	return cmd
}

func newUpdateRunCmd() *cobra.Command {
	var tenant, version, image string
	var all bool
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Update a tenant (or every tenant) to a new GoTrue image/version",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if !all && tenant == "" {
				return fmt.Errorf("either --tenant or --all is required")
			}
			if version == "" && image == "" {
				return fmt.Errorf("--version or --image is required")
			}

			names := []string{tenant}
			if all {
				names, err = backupTargets("", true) // same "every managed tenant" resolution backup uses
				if err != nil {
					return err
				}
			}
			for _, name := range names {
				if err := updateTenant(cfg, name, version, image, timeout); err != nil {
					return fmt.Errorf("updating %q: %w", name, err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant name")
	cmd.Flags().BoolVar(&all, "all", false, "update every gotruectl-managed tenant")
	cmd.Flags().StringVar(&version, "version", "", "supabase/auth tag to update to, e.g. v2.197.0")
	cmd.Flags().StringVar(&image, "image", "", "override the constructed supabase/auth:<version> image entirely (e.g. a self-built or non-GoTrue image, mainly for testing the rollback path)")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "how long to wait for the new container to report healthy before rolling back")
	return cmd
}

func updateTenant(cfg *Config, tenant, version, image string, timeout time.Duration) error {
	containerName := "gotrue-" + tenant
	exists, running, err := containerState(containerName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no such tenant %q", tenant)
	}
	if !running {
		return fmt.Errorf("tenant %q is not running — `tenant start --name %s` first", tenant, tenant)
	}

	targetImage := image
	if targetImage == "" {
		targetImage = "supabase/auth:" + version
	}

	fmt.Println("pulling", targetImage, "...")
	if err := dockerPull(targetImage); err != nil {
		return err // old container never touched
	}

	fmt.Printf("swapping %s to %s ...\n", containerName, targetImage)
	if err := swapContainer(tenant, targetImage, cfg.Network, timeout); err != nil {
		return err
	}

	fmt.Printf("tenant %q updated to %s\n", tenant, targetImage)
	return nil
}

func newUpdateRotateJWTCmd() *cobra.Command {
	var tenant, secret string
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "rotate-jwt-secret",
		Short: "Rotate a tenant's GOTRUE_JWT_SECRET (invalidates all its existing tokens)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if tenant == "" {
				return fmt.Errorf("--tenant is required")
			}
			return rotateTenantJWTSecret(cfg, tenant, secret, timeout)
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant name")
	cmd.Flags().StringVar(&secret, "secret", "", "new secret to use (default: generated)")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "how long to wait for the container to come back healthy before rolling back")
	return cmd
}

func rotateTenantJWTSecret(cfg *Config, tenant, secret string, timeout time.Duration) error {
	containerName := "gotrue-" + tenant
	exists, running, err := containerState(containerName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no such tenant %q", tenant)
	}
	if !running {
		return fmt.Errorf("tenant %q is not running — `tenant start --name %s` first", tenant, tenant)
	}

	envPath, err := tenantEnvPath(tenant)
	if err != nil {
		return err
	}
	oldContent, err := os.ReadFile(envPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", envPath, err)
	}

	if secret == "" {
		secret, err = generateSecret(32)
		if err != nil {
			return err
		}
	}
	newContent, found := replaceEnvValue(string(oldContent), "GOTRUE_JWT_SECRET", secret)
	if !found {
		return fmt.Errorf("GOTRUE_JWT_SECRET not found in %s", envPath)
	}
	if err := os.WriteFile(envPath, []byte(newContent), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", envPath, err)
	}

	image, err := runCapture("", "docker", "inspect", "-f", "{{.Config.Image}}", containerName)
	if err != nil {
		_ = os.WriteFile(envPath, oldContent, 0o600)
		return fmt.Errorf("reading current image for %s: %w", containerName, err)
	}

	fmt.Printf("rotating JWT secret for %q ...\n", tenant)
	if err := swapContainer(tenant, image, cfg.Network, timeout); err != nil {
		// swapContainer already rolled the container back to the old one
		// (old secret) — keep the env file in sync with what's actually running.
		_ = os.WriteFile(envPath, oldContent, 0o600)
		return err
	}

	fmt.Printf("tenant %q JWT secret rotated\n", tenant)
	fmt.Println("note: every previously issued access/refresh token for this tenant is now invalid")
	return nil
}

// swapContainer is the shared blue/green mechanism behind `update run` and
// `update rotate-jwt-secret`: both need to recreate a tenant's container
// (new image, or same image but a changed env file) without ever leaving
// the tenant down or on a broken version. It always uses the tenant's
// current .env file and host port — the only thing that varies is `image`.
func swapContainer(tenant, image, network string, timeout time.Duration) error {
	containerName := "gotrue-" + tenant
	rollbackName := containerName + "-rollback"

	hostPort, err := dockerHostPort(containerName, "9999/tcp")
	if err != nil {
		return fmt.Errorf("reading current port mapping: %w", err)
	}
	envPath, err := tenantEnvPath(tenant)
	if err != nil {
		return err
	}

	if err := dockerRename(containerName, rollbackName); err != nil {
		return fmt.Errorf("starting swap: %w", err)
	}
	if err := runInherit("", "docker", "stop", rollbackName); err != nil {
		_ = dockerRename(rollbackName, containerName) // never leave it orphaned under the rollback name
		return fmt.Errorf("stopping old container to free its port: %w", err)
	}

	rollback := func(cause error) error {
		_ = runInherit("", "docker", "rm", "-f", containerName)
		if err := dockerRename(rollbackName, containerName); err != nil {
			return fmt.Errorf("rollback could not rename %s back to %s — manual fix needed (docker rename %s %s; docker start %s): %w (original failure: %v)",
				rollbackName, containerName, rollbackName, containerName, containerName, err, cause)
		}
		if err := runInherit("", "docker", "start", containerName); err != nil {
			return fmt.Errorf("rollback could not restart %s — manual fix needed (docker start %s): %w (original failure: %v)",
				containerName, containerName, err, cause)
		}
		return fmt.Errorf("swap failed, rolled back to the previous container: %w", cause)
	}

	runErr := runInherit("", "docker", "run", "-d",
		"--name", containerName,
		"--network", network,
		"--label", managedByLabel,
		"--label", "tenant="+tenant,
		"--label", "role=gotrue",
		"-p", hostPort+":9999",
		"--env-file", envPath,
		"--restart", "unless-stopped",
		image,
	)
	if runErr != nil {
		return rollback(fmt.Errorf("starting new container: %w", runErr))
	}

	if !waitForHealthy(hostPort, timeout) {
		return rollback(fmt.Errorf("new container did not become healthy within %s", timeout))
	}

	if err := runInherit("", "docker", "rm", "-f", rollbackName); err != nil {
		fmt.Fprintln(os.Stderr, "warning: swap succeeded but failed to clean up", rollbackName+":", err)
	}
	return nil
}

func waitForHealthy(hostPort string, timeout time.Duration) bool {
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://localhost:" + hostPort + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(1 * time.Second)
	}
	return false
}
