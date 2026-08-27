package main

import (
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"
	"text/tabwriter"
)

var tenantNameRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func runTenantCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: gotruectl tenant create|list|logs|start|stop|delete")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "create":
		return tenantCreate(rest)
	case "list":
		return tenantList(rest)
	case "logs":
		return tenantLogs(rest)
	case "start":
		return tenantStartStop(rest, true)
	case "stop":
		return tenantStartStop(rest, false)
	case "delete":
		return tenantDelete(rest)
	default:
		return fmt.Errorf("unknown tenant subcommand %q (want create|list|logs|start|stop|delete)", sub)
	}
}

func tenantCreate(args []string) error {
	fs := flag.NewFlagSet("tenant create", flag.ExitOnError)
	name := fs.String("name", "", "tenant name, e.g. kyc or admin (lowercase, starts with a letter)")
	port := fs.Int("port", 0, "host port GoTrue will listen on")
	signup := fs.Bool("signup", false, "allow public self-signup (default: disabled, admin-provisioned only)")
	siteURL := fs.String("site-url", "", "frontend origin GoTrue redirects to (default http://localhost:5173)")
	externalURL := fs.String("external-url", "", "public URL of this GoTrue instance (default derived from --port)")
	jwtSecret := fs.String("jwt-secret", "", "JWT signing secret (default: generated)")
	jwtAud := fs.String("jwt-aud", "authenticated", "JWT audience claim")
	fs.Parse(args)

	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })

	if err := dockerAvailable(); err != nil {
		return err
	}

	tenantName := *name
	if tenantName == "" {
		tenantName = promptRequired("tenant name (lowercase, e.g. kyc)")
	}
	if !tenantNameRe.MatchString(tenantName) {
		return fmt.Errorf("invalid tenant name %q: must start with a letter and contain only lowercase letters, digits, underscore", tenantName)
	}

	tenantPort := *port
	if !set["port"] {
		tenantPort = promptRequiredInt("port")
	}
	if tenantPort <= 0 {
		return fmt.Errorf("invalid port %d", tenantPort)
	}

	resolvedExternalURL := *externalURL
	if !set["external-url"] {
		resolvedExternalURL = promptString("external URL", fmt.Sprintf("http://localhost:%d", tenantPort))
	}
	if resolvedExternalURL == "" {
		resolvedExternalURL = fmt.Sprintf("http://localhost:%d", tenantPort)
	}

	resolvedSiteURL := *siteURL
	if !set["site-url"] {
		resolvedSiteURL = promptString("site URL (frontend origin)", "http://localhost:5173")
	}
	if resolvedSiteURL == "" {
		resolvedSiteURL = "http://localhost:5173"
	}

	generatedSecret, err := generateSecret(32)
	if err != nil {
		return err
	}
	resolvedJWTSecret := *jwtSecret
	if !set["jwt-secret"] {
		resolvedJWTSecret = promptString("JWT secret", generatedSecret)
	}
	if resolvedJWTSecret == "" {
		resolvedJWTSecret = generatedSecret
	}

	resolvedJWTAud := *jwtAud
	if !set["jwt-aud"] {
		resolvedJWTAud = promptString("JWT audience", "authenticated")
	}

	resolvedSignup := *signup
	if !set["signup"] {
		resolvedSignup = promptYesNo("Allow public signup", false)
	}

	containerName := "gotrue-" + tenantName
	exists, _, err := containerState(containerName)
	if err != nil {
		return fmt.Errorf("checking for existing tenant: %w", err)
	}
	if exists {
		return fmt.Errorf("tenant %q already exists (container %s) — use `tenant start` or delete it first", tenantName, containerName)
	}

	if err := postgresUp(); err != nil {
		return fmt.Errorf("ensuring postgres is up: %w", err)
	}

	dbRole := "gotrue_" + tenantName
	dbPassword, err := generateSecret(24)
	if err != nil {
		return err
	}
	fmt.Printf("provisioning database role/db/schema for %s ...\n", dbRole)
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
	envContent := strings.Join([]string{
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
		"",
	}, "\n")
	if err := os.WriteFile(envPath, []byte(envContent), 0o600); err != nil {
		return fmt.Errorf("writing %s: %w", envPath, err)
	}

	fmt.Println("starting", containerName, "...")
	if err := runInherit("", "docker", "run", "-d",
		"--name", containerName,
		"--network", networkName,
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

	fmt.Printf("tenant %q up: %s -> http://localhost:%d\n", tenantName, containerName, tenantPort)
	fmt.Println("reminder: do not expose the /admin/* routes on this port to the public internet")
	return nil
}

// ensureTenantDB idempotently creates the login role, its owned database,
// and the `auth` schema GoTrue's migrations expect to already exist.
func ensureTenantDB(role, password string) error {
	roleExists, err := psqlBoolQuery(fmt.Sprintf("SELECT 1 FROM pg_roles WHERE rolname='%s'", role))
	if err != nil {
		return err
	}
	if !roleExists {
		if err := dockerExecInherit(postgresContainerName, "psql", "-U", "postgres", "-c",
			fmt.Sprintf("CREATE ROLE %s LOGIN PASSWORD '%s';", role, password),
		); err != nil {
			return fmt.Errorf("creating role %s: %w", role, err)
		}
	} else {
		// Re-sync the password every time: the caller always generates a fresh
		// one for the tenant's env file, so an existing role (e.g. kept via
		// `tenant delete --keep-data`) must be updated to match or GoTrue's
		// DATABASE_URL auth fails against the old password.
		if err := dockerExecInherit(postgresContainerName, "psql", "-U", "postgres", "-c",
			fmt.Sprintf("ALTER ROLE %s PASSWORD '%s';", role, password),
		); err != nil {
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

func tenantList(args []string) error {
	containers, err := listContainersByLabel(managedByLabel, "role=gotrue")
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		fmt.Println("no tenants")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPORT\tSTATE\tSTATUS\tIMAGE")
	for _, c := range containers {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", c.label("tenant"), extractHostPort(c.Ports), c.State, c.Status, c.Image)
	}
	return w.Flush()
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

func tenantLogs(args []string) error {
	fs := flag.NewFlagSet("tenant logs", flag.ExitOnError)
	name := fs.String("name", "", "tenant name")
	follow := fs.Bool("follow", false, "stream logs (like docker logs -f)")
	fs.Parse(args)
	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	containerName := "gotrue-" + *name
	exists, _, err := containerState(containerName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no such tenant %q", *name)
	}

	logArgs := []string{"logs"}
	if *follow {
		logArgs = append(logArgs, "-f")
	}
	logArgs = append(logArgs, containerName)
	return runInherit("", "docker", logArgs...)
}

func tenantStartStop(args []string, start bool) error {
	fs := flag.NewFlagSet("tenant start/stop", flag.ExitOnError)
	name := fs.String("name", "", "tenant name")
	fs.Parse(args)
	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	containerName := "gotrue-" + *name
	exists, _, err := containerState(containerName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no such tenant %q", *name)
	}

	action, verb := "stop", "stopped"
	if start {
		action, verb = "start", "started"
	}
	if err := runInherit("", "docker", action, containerName); err != nil {
		return fmt.Errorf("%s %s: %w", action, containerName, err)
	}
	fmt.Printf("%s %s\n", containerName, verb)
	return nil
}

func tenantDelete(args []string) error {
	fs := flag.NewFlagSet("tenant delete", flag.ExitOnError)
	name := fs.String("name", "", "tenant name")
	keepData := fs.Bool("keep-data", false, "keep the tenant's database and role")
	fs.Parse(args)
	if *name == "" {
		return fmt.Errorf("--name is required")
	}

	containerName := "gotrue-" + *name
	exists, _, err := containerState(containerName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no such tenant %q", *name)
	}

	if err := runInherit("", "docker", "rm", "-f", containerName); err != nil {
		return fmt.Errorf("removing %s: %w", containerName, err)
	}

	if envPath, err := tenantEnvPath(*name); err == nil {
		_ = os.Remove(envPath)
	}

	if *keepData {
		fmt.Printf("tenant %q deleted (data kept: role/db gotrue_%s)\n", *name, *name)
		return nil
	}

	_, pgRunning, err := containerState(postgresContainerName)
	if err != nil || !pgRunning {
		fmt.Printf("tenant %q deleted; postgres isn't running so its database/role were left in place — run `postgres up` then `tenant delete --name %s` again to clean those up\n", *name, *name)
		return nil
	}

	dbRole := "gotrue_" + *name
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

	fmt.Printf("tenant %q deleted (database and role dropped)\n", *name)
	return nil
}
