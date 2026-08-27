package gotruectl

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

var tenantNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func newTenantCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "tenant",
		Short: "Manage per-tenant GoTrue containers (create, list, logs, start, stop, delete)",
	}
	cmd.AddCommand(newTenantCreateCmd())
	cmd.AddCommand(newTenantListCmd())
	cmd.AddCommand(newTenantConfigCmd())
	cmd.AddCommand(newTenantLogsCmd())
	cmd.AddCommand(newTenantStartCmd())
	cmd.AddCommand(newTenantStopCmd())
	cmd.AddCommand(newTenantDeleteCmd())
	return cmd
}

func newTenantCreateCmd() *cobra.Command {
	var (
		name           string
		port           int
		signup         bool
		siteURL        string
		externalURL    string
		jwtSecret      string
		jwtAud         string
		smtpHost       string
		smtpPort       string
		smtpUser       string
		smtpPass       string
		smtpAdminEmail string
		smtpSenderName string
	)
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create and start a new tenant's GoTrue container",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			f := cmd.Flags()
			opts := tenantCreateOpts{
				name:           name,
				nameSet:        f.Changed("name"),
				port:           port,
				portSet:        f.Changed("port"),
				signup:         signup,
				signupSet:      f.Changed("signup"),
				siteURL:        siteURL,
				siteURLSet:     f.Changed("site-url"),
				externalURL:    externalURL,
				externalURLSet: f.Changed("external-url"),
				jwtSecret:      jwtSecret,
				jwtSecretSet:   f.Changed("jwt-secret"),
				jwtAud:         jwtAud,
				jwtAudSet:      f.Changed("jwt-aud"),
				smtpHost:       smtpHost,
				smtpPort:       smtpPort,
				smtpUser:       smtpUser,
				smtpPass:       smtpPass,
				smtpAdminEmail: smtpAdminEmail,
				smtpSenderName: smtpSenderName,
			}
			if f.Changed("smtp-host") {
				opts.smtpHostSet = true
			}
			return tenantCreate(cfg, opts)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "tenant name, e.g. kyc or admin (lowercase, starts with a letter)")
	cmd.Flags().IntVar(&port, "port", 0, "host port GoTrue will listen on")
	cmd.Flags().BoolVar(&signup, "signup", false, "allow public self-signup (default: disabled, admin-provisioned only)")
	cmd.Flags().StringVar(&siteURL, "site-url", "", "frontend origin GoTrue redirects to (default from config)")
	cmd.Flags().StringVar(&externalURL, "external-url", "", "public URL of this GoTrue instance (default derived from --port)")
	cmd.Flags().StringVar(&jwtSecret, "jwt-secret", "", "JWT signing secret (default: generated)")
	cmd.Flags().StringVar(&jwtAud, "jwt-aud", "", "JWT audience claim (default from config)")
	cmd.Flags().StringVar(&smtpHost, "smtp-host", "", "override the configured SMTP host for this tenant only")
	cmd.Flags().StringVar(&smtpPort, "smtp-port", "", "override the configured SMTP port for this tenant only")
	cmd.Flags().StringVar(&smtpUser, "smtp-user", "", "override the configured SMTP user for this tenant only")
	cmd.Flags().StringVar(&smtpPass, "smtp-pass", "", "override the configured SMTP password for this tenant only")
	cmd.Flags().StringVar(&smtpAdminEmail, "smtp-admin-email", "", "override the configured SMTP from-address for this tenant only")
	cmd.Flags().StringVar(&smtpSenderName, "smtp-sender-name", "", "override the configured SMTP from-name for this tenant only")
	return cmd
}

type tenantCreateOpts struct {
	name           string
	nameSet        bool
	port           int
	portSet        bool
	signup         bool
	signupSet      bool
	siteURL        string
	siteURLSet     bool
	externalURL    string
	externalURLSet bool
	jwtSecret      string
	jwtSecretSet   bool
	jwtAud         string
	jwtAudSet      bool
	smtpHost       string
	smtpHostSet    bool
	smtpPort       string
	smtpUser       string
	smtpPass       string
	smtpAdminEmail string
	smtpSenderName string
}

func tenantCreate(cfg *Config, o tenantCreateOpts) error {
	if err := dockerAvailable(); err != nil {
		return err
	}

	tenantName := o.name
	if !o.nameSet || tenantName == "" {
		tenantName = promptRequired("tenant name (lowercase, e.g. kyc)")
	}
	if !tenantNameRe.MatchString(tenantName) {
		return fmt.Errorf("invalid tenant name %q: must start with a letter and contain only lowercase letters, digits, underscore", tenantName)
	}

	tenantPort := o.port
	if !o.portSet {
		tenantPort = promptRequiredInt("port")
	}
	if tenantPort <= 0 {
		return fmt.Errorf("invalid port %d", tenantPort)
	}

	resolvedExternalURL := o.externalURL
	if !o.externalURLSet {
		resolvedExternalURL = promptString("external URL", fmt.Sprintf("http://localhost:%d", tenantPort))
	}
	if resolvedExternalURL == "" {
		resolvedExternalURL = fmt.Sprintf("http://localhost:%d", tenantPort)
	}

	resolvedSiteURL := o.siteURL
	if !o.siteURLSet {
		resolvedSiteURL = promptString("site URL (frontend origin)", cfg.DefaultSiteURL)
	}
	if resolvedSiteURL == "" {
		resolvedSiteURL = cfg.DefaultSiteURL
	}

	generatedSecret, err := generateSecret(32)
	if err != nil {
		return err
	}
	resolvedJWTSecret := o.jwtSecret
	if !o.jwtSecretSet {
		resolvedJWTSecret = promptSecret("JWT secret", generatedSecret)
	}
	if resolvedJWTSecret == "" {
		resolvedJWTSecret = generatedSecret
	}

	resolvedJWTAud := o.jwtAud
	if !o.jwtAudSet {
		resolvedJWTAud = promptString("JWT audience", cfg.DefaultJWTAud)
	}
	if resolvedJWTAud == "" {
		resolvedJWTAud = cfg.DefaultJWTAud
	}

	resolvedSignup := o.signup
	if !o.signupSet {
		resolvedSignup = promptYesNo("Allow public signup", false)
	}

	resolvedSMTPHost := cfg.SMTPHost
	resolvedSMTPPort := cfg.SMTPPort
	resolvedSMTPUser := cfg.SMTPUser
	resolvedSMTPPass := cfg.SMTPPass
	resolvedSMTPAdminEmail := cfg.SMTPAdminEmail
	resolvedSMTPSenderName := cfg.SMTPSenderName
	if o.smtpHostSet {
		resolvedSMTPHost = o.smtpHost
	}
	if o.smtpPort != "" {
		resolvedSMTPPort = o.smtpPort
	}
	if o.smtpUser != "" {
		resolvedSMTPUser = o.smtpUser
	}
	if o.smtpPass != "" {
		resolvedSMTPPass = o.smtpPass
	}
	if o.smtpAdminEmail != "" {
		resolvedSMTPAdminEmail = o.smtpAdminEmail
	}
	if o.smtpSenderName != "" {
		resolvedSMTPSenderName = o.smtpSenderName
	}

	containerName := "gotrue-" + tenantName
	exists, _, err := containerState(containerName)
	if err != nil {
		return fmt.Errorf("checking for existing tenant: %w", err)
	}
	if exists {
		return fmt.Errorf("tenant %q already exists (container %s) — use `tenant start` or delete it first", tenantName, containerName)
	}

	if err := postgresUp(cfg); err != nil {
		return fmt.Errorf("ensuring postgres is up: %w", err)
	}

	dbRole := "gotrue_" + tenantName
	dbPassword, err := generateSecret(24)
	if err != nil {
		return err
	}
	printMuted("provisioning database role/db/schema for %s ...", dbRole)
	if err := ensureTenantDB(dbRole, dbPassword); err != nil {
		return err
	}

	disableSignup := "true"
	if resolvedSignup {
		disableSignup = "false"
	}
	databaseURL := fmt.Sprintf(
		"postgres://%s:%s@%s:5432/%s?sslmode=disable&search_path=auth,public",
		dbRole, dbPassword, postgresContainerName, dbRole,
	)

	envDir, err := tenantsDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(envDir, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", envDir, err)
	}
	envPath, err := tenantEnvPath(tenantName)
	if err != nil {
		return err
	}
	envLines := []string{
		"GOTRUE_API_HOST=0.0.0.0",
		"PORT=9999",
		"API_EXTERNAL_URL=" + resolvedExternalURL,
		"GOTRUE_DB_DRIVER=postgres",
		"DATABASE_URL=" + databaseURL,
		"GOTRUE_DB_NAMESPACE=auth",
		"GOTRUE_JWT_SECRET=" + resolvedJWTSecret,
		"GOTRUE_JWT_EXP=3600",
		"GOTRUE_JWT_AUD=" + resolvedJWTAud,
		"GOTRUE_SITE_URL=" + resolvedSiteURL,
		"GOTRUE_DISABLE_SIGNUP=" + disableSignup,
	}
	if resolvedSMTPHost != "" {
		envLines = append(envLines,
			"GOTRUE_EXTERNAL_EMAIL_ENABLED=true",
			"GOTRUE_SMTP_HOST="+resolvedSMTPHost,
			"GOTRUE_SMTP_PORT="+resolvedSMTPPort,
			"GOTRUE_SMTP_USER="+resolvedSMTPUser,
			"GOTRUE_SMTP_PASS="+resolvedSMTPPass,
			"GOTRUE_SMTP_ADMIN_EMAIL="+resolvedSMTPAdminEmail,
			"GOTRUE_SMTP_SENDER_NAME="+resolvedSMTPSenderName,
		)
	}
	envLines = append(envLines, "")
	envContent := strings.Join(envLines, "\n")
	if err := os.WriteFile(envPath, []byte(envContent), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", envPath, err)
	}

	printMuted("starting %s ...", containerName)
	if err := runInherit("", "docker", "run", "-d",
		"--name", containerName,
		"--network", cfg.Network,
		"--label", managedByLabel,
		"--label", "tenant="+tenantName,
		"--label", "role=gotrue",
		"-p", fmt.Sprintf("%d:9999", tenantPort),
		"--env-file", envPath,
		"--restart", "unless-stopped",
		gotrueImage,
	); err != nil {
		return fmt.Errorf("running %s: %w", containerName, err)
	}

	printSuccess("tenant %q up: %s -> http://localhost:%d", tenantName, containerName, tenantPort)
	printWarn("reminder: do not expose the /admin/* routes on this port to the public internet")
	return nil
}

// ensureTenantDB idempotently creates the login role, its owned database,
// and the `auth` schema GoTrue's migrations expect to already exist.
func ensureTenantDB(role, password string) error {
	roleExists, err := psqlBoolQuery(fmt.Sprintf("SELECT 1 FROM pg_roles WHERE rolname='%s'", role))
	if err != nil {
		return err
	}
	// The password is fed over stdin (dockerExecInheritStdin), not as a "-c"
	// argument — a "-c 'CREATE ROLE ... PASSWORD ...'" argument would put the
	// plaintext password in argv, visible to any local user via `ps aux` for
	// as long as psql is running.
	if !roleExists {
		sql := fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s';", role, password)
		if err := dockerExecInheritStdin(postgresContainerName, sql, "psql", "-U", "postgres"); err != nil {
			return fmt.Errorf("creating role %s: %w", role, err)
		}
	} else {
		// Re-sync the password every time: the caller always generates a fresh
		// one for the tenant's env file, so an existing role (e.g. kept via
		// `tenant delete --keep-data`) must be updated to match or GoTrue's
		// DATABASE_URL auth fails against the old password.
		sql := fmt.Sprintf("ALTER ROLE %s PASSWORD '%s';", role, password)
		if err := dockerExecInheritStdin(postgresContainerName, sql, "psql", "-U", "postgres"); err != nil {
			return fmt.Errorf("updating password for role %s: %w", role, err)
		}
	}

	dbExists, err := psqlBoolQuery(fmt.Sprintf("SELECT 1 FROM pg_database WHERE datname='%s'", role))
	if err != nil {
		return err
	}
	if !dbExists {
		if err := dockerExecInherit(postgresContainerName, "psql", "-U", "postgres", "-c",
			fmt.Sprintf("CREATE DATABASE %s OWNER %s;", role, role),
		); err != nil {
			return fmt.Errorf("creating database %s: %w", role, err)
		}
	}

	if err := dockerExecInherit(postgresContainerName, "psql", "-U", "postgres", "-d", role, "-c",
		fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS auth AUTHORIZATION %s;", role),
	); err != nil {
		return fmt.Errorf("creating schema for %s: %w", role, err)
	}
	return nil
}

func psqlBoolQuery(query string) (bool, error) {
	out, err := dockerExecCapture(postgresContainerName, "psql", "-U", "postgres", "-tAc", query)
	if err != nil {
		return false, fmt.Errorf("querying postgres: %w", err)
	}
	return strings.TrimSpace(out) == "1", nil
}

func newTenantListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List gotruectl-managed tenants",
		RunE: func(cmd *cobra.Command, args []string) error {
			return tenantList()
		},
	}
}

func tenantList() error {
	containers, err := listContainersByLabel(managedByLabel, "role=gotrue")
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		printMuted("no tenants")
		return nil
	}

	rows := make([][]string, 0, len(containers))
	for _, c := range containers {
		rows = append(rows, []string{c.label("tenant"), extractHostPort(c.Ports), c.State, c.Status, c.Image})
	}
	fmt.Println(renderTable([]string{"NAME", "PORT", "STATE", "STATUS", "IMAGE"}, rows))
	return nil
}

// extractHostPort pulls the host-side port out of a docker ps Ports string
// such as "0.0.0.0:9999->9999/tcp, :::9999->9999/tcp".
func extractHostPort(ports string) string {
	first := strings.TrimSpace(strings.Split(ports, ",")[0])
	arrow := strings.Index(first, "->")
	if arrow == -1 {
		return "-"
	}
	hostPart := first[:arrow]
	colon := strings.LastIndex(hostPart, ":")
	if colon == -1 {
		return hostPart
	}
	return hostPart[colon+1:]
}

func newTenantLogsCmd() *cobra.Command {
	var name string
	var follow bool
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show a tenant's GoTrue container logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			return tenantLogs(name, follow)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "tenant name")
	cmd.Flags().BoolVar(&follow, "follow", false, "stream logs (like docker logs -f)")
	return cmd
}

func tenantLogs(name string, follow bool) error {
	containerName := "gotrue-" + name
	exists, _, err := containerState(containerName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no such tenant %q", name)
	}

	logArgs := []string{"logs"}
	if follow {
		logArgs = append(logArgs, "-f")
	}
	logArgs = append(logArgs, containerName)
	return runInherit("", "docker", logArgs...)
}

func newTenantStartCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "start",
		Short: "Start a stopped tenant's GoTrue container",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			return tenantStartStop(name, true)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "tenant name")
	return cmd
}

func newTenantStopCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use:   "stop",
		Short: "Stop a running tenant's GoTrue container",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			return tenantStartStop(name, false)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "tenant name")
	return cmd
}

func tenantStartStop(name string, start bool) error {
	containerName := "gotrue-" + name
	exists, _, err := containerState(containerName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no such tenant %q", name)
	}

	action, verb := "stop", "stopped"
	if start {
		action, verb = "start", "started"
	}
	if err := runInherit("", "docker", action, containerName); err != nil {
		return fmt.Errorf("%s %s: %w", action, containerName, err)
	}
	printSuccess("%s %s", containerName, verb)
	return nil
}

func newTenantDeleteCmd() *cobra.Command {
	var name string
	var keepData bool
	cmd := &cobra.Command{
		Use:   "delete",
		Short: "Remove a tenant's GoTrue container (and, by default, its database)",
		RunE: func(cmd *cobra.Command, args []string) error {
			if name == "" {
				return fmt.Errorf("--name is required")
			}
			return tenantDelete(name, keepData)
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "tenant name")
	cmd.Flags().BoolVar(&keepData, "keep-data", false, "keep the tenant's database and role")
	return cmd
}

func tenantDelete(name string, keepData bool) error {
	containerName := "gotrue-" + name
	exists, _, err := containerState(containerName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no such tenant %q", name)
	}

	if err := runInherit("", "docker", "rm", "-f", containerName); err != nil {
		return fmt.Errorf("removing %s: %w", containerName, err)
	}

	if envPath, err := tenantEnvPath(name); err == nil {
		_ = os.Remove(envPath)
	}

	if keepData {
		printSuccess("tenant %q deleted (data kept: role/db gotrue_%s)", name, name)
		return nil
	}

	_, pgRunning, err := containerState(postgresContainerName)
	if err != nil || !pgRunning {
		printWarn("tenant %q deleted; postgres isn't running so its database/role were left in place — run `postgres up` then `tenant delete --name %s` again to clean those up", name, name)
		return nil
	}

	dbRole := "gotrue_" + name
	if err := dockerExecInherit(postgresContainerName, "psql", "-U", "postgres", "-c",
		fmt.Sprintf("DROP DATABASE IF EXISTS %s;", dbRole),
	); err != nil {
		return fmt.Errorf("dropping database %s: %w", dbRole, err)
	}
	if err := dockerExecInherit(postgresContainerName, "psql", "-U", "postgres", "-c",
		fmt.Sprintf("DROP ROLE IF EXISTS %s;", dbRole),
	); err != nil {
		return fmt.Errorf("dropping role %s: %w", dbRole, err)
	}

	printSuccess("tenant %q deleted (database and role dropped)", name)
	return nil
}
