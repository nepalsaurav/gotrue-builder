package gotruectl

import (
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"
)

func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Back up and list tenant user data (the auth schema)",
	}
	cmd.AddCommand(newBackupRunCmd())
	cmd.AddCommand(newBackupListCmd())
	return cmd
}

func newBackupRunCmd() *cobra.Command {
	var tenant string
	var all bool
	cmd := &cobra.Command{
		Use:   "run",
		Short: "Dump a tenant's (or every tenant's) auth schema to a timestamped, gzipped file",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if !all && tenant == "" {
				return fmt.Errorf("either --tenant or --all is required")
			}
			names, err := backupTargets(tenant, all)
			if err != nil {
				return err
			}
			for _, name := range names {
				if err := backupTenant(cfg, name); err != nil {
					return fmt.Errorf("backing up %q: %w", name, err)
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant name")
	cmd.Flags().BoolVar(&all, "all", false, "back up every gotruectl-managed tenant")
	return cmd
}

func backupTargets(tenant string, all bool) ([]string, error) {
	if !all {
		return []string{tenant}, nil
	}
	containers, err := listContainersByLabel(managedByLabel, "role=gotrue")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, c := range containers {
		if t := c.label("tenant"); t != "" {
			names = append(names, t)
		}
	}
	if len(names) == 0 {
		return nil, fmt.Errorf("no tenants to back up")
	}
	return names, nil
}

// backupTenant dumps the `auth` schema (users, identities, sessions,
// refresh_tokens, mfa factors — everything GoTrue owns) for one tenant.
// pg_dump runs inside the shared postgres container, so no DB driver
// dependency is needed here; its stdout is piped straight through gzip to
// the destination file.
func backupTenant(cfg *Config, tenant string) error {
	dbRole := "gotrue_" + tenant
	exists, err := psqlBoolQuery(fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname='%s'", dbRole))
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no database %q — has tenant %q been created?", dbRole, tenant)
	}

	dir := tenantBackupDir(cfg.BackupDir, tenant)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	timestamp := time.Now().UTC().Format("20060102T150405Z")
	destPath := filepath.Join(dir, fmt.Sprintf("%s-%s.sql.gz", tenant, timestamp))

	dump, err := dockerExecCapture(postgresContainerName, "pg_dump", "-U", "postgres", "-d", dbRole, "--schema=auth", "--no-owner")
	if err != nil {
		return fmt.Errorf("pg_dump: %w", err)
	}

	f, err := os.OpenFile(destPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("creating %s: %w", destPath, err)
	}
	defer f.Close()

	gz := gzip.NewWriter(f)
	if _, err := gz.Write([]byte(dump)); err != nil {
		return fmt.Errorf("writing %s: %w", destPath, err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("finalizing %s: %w", destPath, err)
	}

	printSuccess("backed up %q -> %s", tenant, destPath)
	return nil
}

func newBackupListCmd() *cobra.Command {
	var tenant string
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List existing backups",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			return backupList(cfg, tenant)
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "only list this tenant's backups")
	return cmd
}

func backupList(cfg *Config, tenant string) error {
	type entry struct {
		tenant string
		path   string
		info   os.FileInfo
	}
	var entries []entry

	tenants := []string{tenant}
	if tenant == "" {
		dirEntries, err := os.ReadDir(cfg.BackupDir)
		if err != nil {
			if os.IsNotExist(err) {
				printMuted("no backups yet")
				return nil
			}
			return fmt.Errorf("reading %s: %w", cfg.BackupDir, err)
		}
		tenants = nil
		for _, de := range dirEntries {
			if de.IsDir() {
				tenants = append(tenants, de.Name())
			}
		}
	}

	for _, t := range tenants {
		dir := tenantBackupDir(cfg.BackupDir, t)
		files, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, f := range files {
			info, err := f.Info()
			if err != nil {
				continue
			}
			entries = append(entries, entry{tenant: t, path: filepath.Join(dir, f.Name()), info: info})
		}
	}

	if len(entries) == 0 {
		printMuted("no backups yet")
		return nil
	}

	sort.Slice(entries, func(i, j int) bool { return entries[i].info.ModTime().Before(entries[j].info.ModTime()) })
	rows := make([][]string, 0, len(entries))
	for _, e := range entries {
		rows = append(rows, []string{e.tenant, fmt.Sprintf("%d bytes", e.info.Size()), e.info.ModTime().Format(time.RFC3339), e.path})
	}
	fmt.Println(renderTable([]string{"TENANT", "SIZE", "MODIFIED", "PATH"}, rows))
	return nil
}
