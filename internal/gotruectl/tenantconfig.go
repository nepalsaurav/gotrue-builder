package gotruectl

import (
	"fmt"
	"sort"
	"time"

	"github.com/spf13/cobra"
)

func newTenantConfigCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show one tenant's full GoTrue configuration",
		Long: `Unlike "tenant list" (docker-level: state/status/image), this reads the
tenant's own .env file and shows every GoTrue-level setting that actually
governs its behavior — not a curated subset. Secrets (JWT secret, DB
password embedded in DATABASE_URL, SMTP password) are masked to "(set)";
use "gotruectl key" for the JWT, or read the .env file directly
(~/.gotrue-builder/tenants/<name>.env) if you need the actual value to
configure another app.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			return tenantConfigShow(name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "tenant name (required)")
	cmd.AddCommand(newTenantConfigSetCmd())
	return cmd
}

// tenantConfigKeyOrder groups settings by concern (network, auth, database,
// mail) rather than alphabetically, so the vertical output reads like a
// mental model of the tenant top-to-bottom instead of a random env dump.
var tenantConfigKeyOrder = []string{
	"API_EXTERNAL_URL", "GOTRUE_SITE_URL", "GOTRUE_API_HOST", "PORT",
	"GOTRUE_JWT_AUD", "GOTRUE_JWT_EXP", "GOTRUE_JWT_SECRET",
	"GOTRUE_DISABLE_SIGNUP",
	"GOTRUE_DB_DRIVER", "GOTRUE_DB_NAMESPACE", "DATABASE_URL",
	"GOTRUE_EXTERNAL_EMAIL_ENABLED", "GOTRUE_SMTP_HOST", "GOTRUE_SMTP_PORT",
	"GOTRUE_SMTP_USER", "GOTRUE_SMTP_PASS", "GOTRUE_SMTP_ADMIN_EMAIL", "GOTRUE_SMTP_SENDER_NAME",
}

var tenantConfigSecretKeys = map[string]bool{
	"GOTRUE_JWT_SECRET": true,
	"DATABASE_URL":      true,
	"GOTRUE_SMTP_PASS":  true,
}

func tenantConfigShow(name string) error {
	containerName := "gotrue-" + name
	exists, _, err := containerState(containerName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no such tenant %q", name)
	}

	envPath, err := tenantEnvPath(name)
	if err != nil {
		return err
	}
	env, err := parseEnvFile(envPath)
	if err != nil {
		return fmt.Errorf("reading %s: %w", envPath, err)
	}

	containers, err := listContainersByLabel(managedByLabel, "role=gotrue")
	if err != nil {
		return err
	}
	var hostPort, state, status, image string
	for _, c := range containers {
		if c.label("tenant") == name {
			hostPort, state, status, image = extractHostPort(c.Ports), c.State, c.Status, c.Image
		}
	}

	rows := [][]string{
		{"TENANT", name},
		{"CONTAINER", containerName},
		{"STATE", state},
		{"STATUS", status},
		{"IMAGE", image},
		{"HOST_PORT", hostPort},
	}

	seen := map[string]bool{}
	for _, key := range tenantConfigKeyOrder {
		v, ok := env[key]
		if !ok {
			continue
		}
		seen[key] = true
		if tenantConfigSecretKeys[key] {
			v = maskSecret(v)
		}
		rows = append(rows, []string{key, v})
		if key == "GOTRUE_DISABLE_SIGNUP" {
			signup := "enabled"
			if v != "false" {
				signup = "disabled"
			}
			rows = append(rows, []string{"  (public signup)", signup})
		}
	}

	// Anything in the file but not in the known order — e.g. a GOTRUE_*
	// setting added by hand — still shows up, sorted, so this view can
	// never hide a real setting the way the old curated-column table did.
	var extra []string
	for k := range env {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(extra)
	for _, key := range extra {
		v := env[key]
		if tenantConfigSecretKeys[key] {
			v = maskSecret(v)
		}
		rows = append(rows, []string{key, v})
	}

	fmt.Println(renderTable([]string{"SETTING", "VALUE"}, rows))
	printMuted("secrets are masked — `gotruectl key --tenant %s` or the .env file has the real values", name)
	return nil
}

func newTenantConfigSetCmd() *cobra.Command {
	var (
		name           string
		siteURL        string
		externalURL    string
		jwtAud         string
		signup         bool
		smtpHost       string
		smtpPort       string
		smtpUser       string
		smtpPass       string
		smtpAdminEmail string
		smtpSenderName string
		timeout        time.Duration
	)
	cmd := &cobra.Command{
		Use:   "set",
		Short: "Change a tenant's GoTrue config and safely restart it to apply",
		Long: `Edits the tenant's .env file and, if anything actually changed, applies it
via the same blue/green swap (with automatic rollback) that "update run"
uses — same image, just the updated env. Only pass the flags you want to
change; everything else is left as-is. Note: the host port and JWT secret
aren't changeable here — recreate the tenant for the former, use
"update rotate-jwt-secret" for the latter (it needs its own "tokens are now
invalid" warning, which a generic config-set command shouldn't bury).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			if name == "" {
				return fmt.Errorf("--name is required")
			}

			f := cmd.Flags()
			changes := map[string]string{}
			if f.Changed("site-url") {
				changes["GOTRUE_SITE_URL"] = siteURL
			}
			if f.Changed("external-url") {
				changes["API_EXTERNAL_URL"] = externalURL
			}
			if f.Changed("jwt-aud") {
				changes["GOTRUE_JWT_AUD"] = jwtAud
			}
			if f.Changed("signup") {
				disable := "true"
				if signup {
					disable = "false"
				}
				changes["GOTRUE_DISABLE_SIGNUP"] = disable
			}
			smtpTouched := false
			if f.Changed("smtp-host") {
				changes["GOTRUE_SMTP_HOST"] = smtpHost
				smtpTouched = true
			}
			if f.Changed("smtp-port") {
				changes["GOTRUE_SMTP_PORT"] = smtpPort
				smtpTouched = true
			}
			if f.Changed("smtp-user") {
				changes["GOTRUE_SMTP_USER"] = smtpUser
				smtpTouched = true
			}
			if f.Changed("smtp-pass") {
				changes["GOTRUE_SMTP_PASS"] = smtpPass
				smtpTouched = true
			}
			if f.Changed("smtp-admin-email") {
				changes["GOTRUE_SMTP_ADMIN_EMAIL"] = smtpAdminEmail
				smtpTouched = true
			}
			if f.Changed("smtp-sender-name") {
				changes["GOTRUE_SMTP_SENDER_NAME"] = smtpSenderName
				smtpTouched = true
			}
			if smtpTouched {
				enabled := "true"
				if f.Changed("smtp-host") && smtpHost == "" {
					enabled = "false"
				}
				changes["GOTRUE_EXTERNAL_EMAIL_ENABLED"] = enabled
			}

			if len(changes) == 0 {
				return fmt.Errorf("no changes given — pass at least one of --site-url, --external-url, --jwt-aud, --signup, --smtp-*")
			}

			changed, err := applyEnvChangesAndRestart(cfg, name, changes, timeout)
			if err != nil {
				return err
			}
			if !changed {
				printMuted("no changes — every requested value already matched")
				return nil
			}
			printSuccess("tenant %q config updated and restarted", name)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "tenant name")
	cmd.Flags().StringVar(&siteURL, "site-url", "", "frontend origin GoTrue redirects to")
	cmd.Flags().StringVar(&externalURL, "external-url", "", "public URL of this GoTrue instance")
	cmd.Flags().StringVar(&jwtAud, "jwt-aud", "", "JWT audience claim")
	cmd.Flags().BoolVar(&signup, "signup", false, "allow public self-signup")
	cmd.Flags().StringVar(&smtpHost, "smtp-host", "", "SMTP host (empty disables email)")
	cmd.Flags().StringVar(&smtpPort, "smtp-port", "", "SMTP port")
	cmd.Flags().StringVar(&smtpUser, "smtp-user", "", "SMTP user")
	cmd.Flags().StringVar(&smtpPass, "smtp-pass", "", "SMTP password")
	cmd.Flags().StringVar(&smtpAdminEmail, "smtp-admin-email", "", "SMTP from-address")
	cmd.Flags().StringVar(&smtpSenderName, "smtp-sender-name", "", "SMTP from-name")
	cmd.Flags().DurationVar(&timeout, "timeout", 30*time.Second, "how long to wait for the container to come back healthy before rolling back")
	return cmd
}
