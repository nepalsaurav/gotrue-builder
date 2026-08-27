package gotruectl

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/spf13/cobra"
)

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check the health of the whole system: docker, postgres, and every tenant",
		Long: `Unlike "status" (what containers exist and their docker state) or "tenant
config" (one tenant's settings), this actively probes each component —
docker reachability, postgres connectivity, each tenant's real HTTP
/health response, and backup freshness — and reports one pass/fail per
check. Exits non-zero if anything failed, so it's safe to use in a cron
job or CI step, not just interactively.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runDoctor()
		},
	}
}

type doctorCheck struct {
	component string
	ok        bool
	warn      bool // true = degraded but not a hard failure (e.g. no backups yet)
	detail    string
}

func runDoctor() error {
	var checks []doctorCheck

	if err := dockerAvailable(); err != nil {
		checks = append(checks, doctorCheck{"docker", false, false, err.Error()})
		printDoctorReport(checks)
		return fmt.Errorf("docker is not available — nothing else can be checked")
	}
	checks = append(checks, doctorCheck{"docker", true, false, "reachable"})

	pgExists, pgRunning, err := containerState(postgresContainerName)
	switch {
	case err != nil:
		checks = append(checks, doctorCheck{"postgres", false, false, err.Error()})
	case !pgExists:
		checks = append(checks, doctorCheck{"postgres", false, false, "not created — run `postgres up`"})
	case !pgRunning:
		checks = append(checks, doctorCheck{"postgres", false, false, "container exists but is stopped"})
	default:
		if _, err := dockerExecCapture(postgresContainerName, "pg_isready", "-U", "postgres"); err != nil {
			checks = append(checks, doctorCheck{"postgres", false, false, "container running but not accepting connections"})
		} else {
			checks = append(checks, doctorCheck{"postgres", true, false, "accepting connections"})
		}
	}

	containers, err := listContainersByLabel(managedByLabel, "role=gotrue")
	if err != nil {
		checks = append(checks, doctorCheck{"tenants", false, false, err.Error()})
	} else if len(containers) == 0 {
		checks = append(checks, doctorCheck{"tenants", true, true, "none created yet"})
	} else {
		cfg, cfgErr := loadConfig()
		for _, c := range containers {
			tenant := c.label("tenant")
			checks = append(checks, tenantHealthCheck(tenant, c))
			if cfgErr == nil {
				checks = append(checks, backupFreshnessCheck(cfg, tenant))
			}
		}
	}

	printDoctorReport(checks)

	for _, c := range checks {
		if !c.ok && !c.warn {
			return fmt.Errorf("one or more checks failed")
		}
	}
	return nil
}

func tenantHealthCheck(tenant string, c containerInfo) doctorCheck {
	component := "tenant " + tenant
	if c.State != "running" {
		return doctorCheck{component, false, false, "container is " + c.State}
	}
	port := extractHostPort(c.Ports)
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://localhost:" + port + "/health")
	if err != nil {
		return doctorCheck{component, false, false, fmt.Sprintf("running but /health unreachable: %v", err)}
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return doctorCheck{component, false, false, fmt.Sprintf("/health returned %s", resp.Status)}
	}
	return doctorCheck{component, true, false, "healthy on :" + port}
}

func backupFreshnessCheck(cfg *Config, tenant string) doctorCheck {
	component := "tenant " + tenant + " backup"
	dir := tenantBackupDir(cfg.BackupDir, tenant)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) == 0 {
		return doctorCheck{component, true, true, "no backups yet — `gotruectl backup run --tenant " + tenant + "`"}
	}
	var newest os.FileInfo
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		if newest == nil || info.ModTime().After(newest.ModTime()) {
			newest = info
		}
	}
	if newest == nil {
		return doctorCheck{component, true, true, "no backups yet"}
	}
	age := time.Since(newest.ModTime())
	detail := fmt.Sprintf("last backup %s ago", age.Round(time.Minute))
	if age > 7*24*time.Hour {
		return doctorCheck{component, true, true, detail + " (getting stale)"}
	}
	return doctorCheck{component, true, false, detail}
}

func printDoctorReport(checks []doctorCheck) {
	rows := make([][]string, 0, len(checks))
	passed, warned, failed := 0, 0, 0
	for _, c := range checks {
		var status string
		switch {
		case c.ok && !c.warn:
			status = successStyle.Render("OK")
			passed++
		case c.warn:
			status = warnStyle.Render("WARN")
			warned++
		default:
			status = errorStyle.Render("FAIL")
			failed++
		}
		rows = append(rows, []string{c.component, status, c.detail})
	}
	fmt.Println(renderTable([]string{"COMPONENT", "STATUS", "DETAIL"}, rows))

	summary := fmt.Sprintf("%d ok, %d warning(s), %d failed", passed, warned, failed)
	switch {
	case failed > 0:
		printError("%s", summary)
	case warned > 0:
		printWarn("%s", summary)
	default:
		printSuccess("%s", summary)
	}
}
