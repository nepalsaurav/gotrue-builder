package gotruectl

import (
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/spf13/cobra"
)

func newBackupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "backup",
		Short: "Back up, restore, and list tenant user data (the auth schema)",
	}
	cmd.AddCommand(newBackupRunCmd())
	cmd.AddCommand(newBackupListCmd())
	cmd.AddCommand(newBackupRestoreCmd())
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

func newBackupRestoreCmd() *cobra.Command {
	var tenant, file string
	var yes bool
	cmd := &cobra.Command{
		Use:   "restore",
		Short: "Restore a tenant's auth schema from a backup file (DESTRUCTIVE)",
		Long: `Replaces everything currently in the tenant's auth schema — every user,
session, identity, and MFA factor — with the contents of a backup file.
This is the riskiest command in gotruectl, so it always:
  1. takes a fresh safety backup of the tenant's CURRENT state first, so a
     restore you didn't mean to run (or ran against the wrong file) can
     itself be undone;
  2. asks for confirmation unless --yes is given;
  3. stops the tenant's container during the restore and starts it back up
     afterward, so GoTrue never serves requests against a half-restored
     schema;
  4. aborts on the first SQL error (psql -v ON_ERROR_STOP=1) rather than
     silently continuing past one.
--file defaults to the tenant's most recent backup if not given.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if tenant == "" {
				return fmt.Errorf("--tenant is required")
			}
			if file == "" {
				file, err = latestBackupFile(cfg, tenant)
				if err != nil {
					return err
				}
			}
			return restoreTenant(cfg, tenant, file, yes)
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant name")
	cmd.Flags().StringVar(&file, "file", "", "backup file to restore (default: the tenant's most recent backup)")
	cmd.Flags().BoolVar(&yes, "yes", false, "skip the confirmation prompt")
	return cmd
}

func latestBackupFile(cfg *Config, tenant string) (string, error) {
	dir := tenantBackupDir(cfg.BackupDir, tenant)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return "", fmt.Errorf("no backups found for tenant %q in %s — run `backup run --tenant %s` first, or pass --file", tenant, dir, tenant)
	}
	var newestName string
	var newestTime time.Time
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newestName == "" || info.ModTime().After(newestTime) {
			newestName, newestTime = e.Name(), info.ModTime()
		}
	}
	return filepath.Join(dir, newestName), nil
}

// restoreTenant replaces the tenant's auth schema with the contents of a
// backup file. The schema is dropped, then the decompressed dump (which
// recreates the schema itself, along with every table) is replayed as one
// psql invocation, run AS the tenant's own role — the same privilege it
// already has from owning the schema, no elevated access needed, and
// everything the dump creates comes out owned by that role automatically.
func restoreTenant(cfg *Config, tenant, backupFile string, autoConfirm bool) error {
	containerName := "gotrue-" + tenant
	exists, running, err := containerState(containerName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no such tenant %q", tenant)
	}

	dbRole := "gotrue_" + tenant
	dbExists, err := psqlBoolQuery(fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname='%s'", dbRole))
	if err != nil {
		return err
	}
	if !dbExists {
		return fmt.Errorf("database %q does not exist — has tenant %q been created?", dbRole, tenant)
	}

	f, err := os.Open(backupFile)
	if err != nil {
		return fmt.Errorf("opening %s: %w", backupFile, err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("reading %s as gzip (is it a gotruectl backup file?): %w", backupFile, err)
	}
	dumpBytes, err := io.ReadAll(gz)
	if err != nil {
		return fmt.Errorf("decompressing %s: %w", backupFile, err)
	}
	if err := gz.Close(); err != nil {
		return fmt.Errorf("finishing decompression of %s: %w", backupFile, err)
	}
	if len(dumpBytes) == 0 {
		return fmt.Errorf("%s decompressed to nothing — refusing to restore an empty dump", backupFile)
	}

	if !autoConfirm {
		printWarn("this REPLACES every user, session, identity, and MFA factor currently in tenant %q with the contents of:", tenant)
		printWarn("  %s", backupFile)
		if !promptYesNo("A safety backup of the current state will be taken first anyway. Continue", false) {
			return fmt.Errorf("restore cancelled")
		}
	}

	printMuted("backing up %q's current state before restoring, in case this needs to be undone ...", tenant)
	if err := backupTenant(cfg, tenant); err != nil {
		return fmt.Errorf("safety backup before restore failed, aborting restore without touching anything: %w", err)
	}

	if running {
		printMuted("stopping %s for the restore ...", containerName)
		if err := runInherit("", "docker", "stop", containerName); err != nil {
			return fmt.Errorf("stopping %s: %w", containerName, err)
		}
		defer func() {
			printMuted("starting %s back up ...", containerName)
			if err := runInherit("", "docker", "start", containerName); err != nil {
				printError("failed to restart %s after restore: %v — start it manually with `tenant start --name %s`", containerName, err, tenant)
			}
		}()
	}

	// pg_dump --schema=auth already emits its own "CREATE SCHEMA auth;" near
	// the top of the dump — running as -U dbRole means that statement
	// creates it owned by the tenant's role with no AUTHORIZATION clause
	// needed. Adding our own CREATE SCHEMA here collided with the dump's
	// (verified live: "ERROR: schema \"auth\" already exists", which aborted
	// the restore before any data was replayed) — only DROP belongs here.
	preamble := "DROP SCHEMA IF EXISTS auth CASCADE;\n"
	fullSQL := preamble + string(dumpBytes)

	printMuted("restoring %q from %s ...", tenant, backupFile)
	if err := dockerExecInheritStdin(postgresContainerName, fullSQL, "psql", "-U", dbRole, "-d", dbRole, "-v", "ON_ERROR_STOP=1"); err != nil {
		return fmt.Errorf("restoring %q failed partway through: %w (the safety backup taken just before this can restore the pre-restore state)", tenant, err)
	}

	printSuccess("tenant %q restored from %s", tenant, backupFile)
	return nil
}
