package main

import (
	"fmt"
	"os"
	"path/filepath"
)

const (
	networkName           = "gotrue-net"
	volumeName            = "gotrue-postgres-data"
	postgresContainerName = "postgres"
	postgresImage         = "postgres:15-alpine"
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
