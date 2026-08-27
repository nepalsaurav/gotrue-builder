package gotruectl

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func newTenantConfigCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show each tenant's GoTrue configuration in a table",
		Long: `Unlike "tenant list" (docker-level: state/status/image), this reads each
tenant's own .env file and shows the GoTrue-level settings that actually
govern its behavior — port, URLs, JWT audience, signup, SMTP.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			return tenantConfigList(name)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "only show this tenant (default: every gotruectl-managed tenant)")
	cmd.AddCommand(newTenantConfigSetCmd())
	return cmd
}

func tenantConfigList(name string) error {
	containers, err := listContainersByLabel(managedByLabel, "role=gotrue")
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		printMuted("no tenants")
		return nil
	}

	var rows [][]string
	for _, c := range containers {
		tenant := c.label("tenant")
		if name != "" && tenant != name {
			continue
		}
		envPath, err := tenantEnvPath(tenant)
		if err != nil {
			return err
		}
		env, err := parseEnvFile(envPath)
		if err != nil {
			rows = append(rows, []string{tenant, extractHostPort(c.Ports), fmt.Sprintf("(could not read env file: %v)", err), "", "", "", ""})
			continue
		}
		signup := "disabled"
		if env["GOTRUE_DISABLE_SIGNUP"] == "false" {
			signup = "enabled"
		}
		smtpHost := env["GOTRUE_SMTP_HOST"]
		if smtpHost == "" {
			smtpHost = "-"
		}
		rows = append(rows, []string{
			tenant, extractHostPort(c.Ports), env["API_EXTERNAL_URL"], env["GOTRUE_SITE_URL"], env["GOTRUE_JWT_AUD"], signup, smtpHost,
		})
	}
	fmt.Println(renderTable([]string{"NAME", "PORT", "EXTERNAL_URL", "SITE_URL", "JWT_AUD", "SIGNUP", "SMTP_HOST"}, rows))
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
