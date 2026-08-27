package gotruectl

import (
	"fmt"
	"os"
	"path/filepath"
)

// postgresContainerName and gotrueImage are fixed identities, not
// user-tunable config: the shared Postgres container is always named
// "postgres" (containers are addressed by name throughout this codebase),
// and gotrueImage is the pinned default version new tenants are created
// with (an explicit --version is required to run a different one via
// `tenant create`'s escape hatch or `update run`). Everything that IS
// meant to be tunable (postgres image, network/volume names, default
// site URL/JWT audience, backup dir) lives in Config (config.go) instead.
const (
	postgresContainerName = "postgres"
	gotrueImage           = "supabase/auth:v2.196.0"
)

func baseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory: %w", err)
	}
	return filepath.Join(home, ".gotrue-builder"), nil
}

func tenantsDir() (string, error) {
	base, err := baseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "tenants"), nil
}

func tenantEnvPath(name string) (string, error) {
	dir, err := tenantsDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, name+".env"), nil
}

func postgresEnvPath() (string, error) {
	base, err := baseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "postgres.env"), nil
}

// tenantBackupDir returns <backupDir>/<tenant>, creating nothing itself.
func tenantBackupDir(backupDir, tenant string) string {
	return filepath.Join(backupDir, tenant)
}
