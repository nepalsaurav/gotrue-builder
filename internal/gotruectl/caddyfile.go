package gotruectl

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// publicGoTrueRoutes are the routes real end users (not your own backend)
// need for normal auth flows — signup, login, password recovery, OTP/email
// verification, MFA enrollment for their OWN factors, and OAuth callbacks.
//
// This is used as an ALLOWLIST — Caddy default-denies (404) anything not
// listed here, including every /admin/* route — rather than as a blocklist
// of admin routes. An allowlist fails safe: a route missing from this list
// just breaks a user-facing flow, visibly, immediately. A blocklist missing
// a route would silently leave it exposed.
//
// Verify this against your actual GoTrue version's route table
// (supabase/auth source) before relying on it in production — exact routes
// can change between versions; this list isn't guaranteed exhaustive.
var publicGoTrueRoutes = []string{
	"/health",
	"/settings",
	"/signup",
	"/recover",
	"/verify",
	"/token",
	"/logout",
	"/user",
	"/otp",
	"/magiclink",
	"/resend",
	"/reauthenticate",
	"/factors*",
	"/authorize",
	"/callback",
	"/sso*",
}

func newCaddyfileCmd() *cobra.Command {
	var tenant, domain, domainTemplate, out string
	var all bool
	cmd := &cobra.Command{
		Use:   "caddyfile",
		Short: "Generate a security-hardened Caddyfile for exposing a tenant publicly",
		Long: `Generates a Caddy reverse-proxy config that forwards ONLY GoTrue's public,
end-user routes (signup/login/recovery/OTP/MFA-for-your-own-factors/OAuth
callback) to the tenant. Everything else — including every /admin/* route
— gets a 404 by default: this is an allowlist, not a blocklist, so a route
this tool doesn't know about fails closed instead of silently staying
exposed.

Never proxy /admin/* publicly. Only your own backend should call it, over
the internal network, using a service_role token minted from the same
GOTRUE_JWT_SECRET (see "gotruectl key" / "gotruectl admin").

This only prints or writes the config — it never touches a running Caddy
install. Validate it yourself before deploying:
  caddy validate --config <file> --adapter caddyfile`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if all {
				if domainTemplate == "" {
					return fmt.Errorf("--domain-template is required with --all (e.g. '{tenant}.auth.example.com')")
				}
				return generateCaddyfileAll(domainTemplate, out)
			}
			if tenant == "" || domain == "" {
				return fmt.Errorf("--tenant and --domain are required (or use --all --domain-template)")
			}
			return generateCaddyfileOne(tenant, domain, out)
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant name")
	cmd.Flags().StringVar(&domain, "domain", "", "public domain to serve this tenant on, e.g. auth.example.com")
	cmd.Flags().BoolVar(&all, "all", false, "generate a block for every gotruectl-managed tenant")
	cmd.Flags().StringVar(&domainTemplate, "domain-template", "", "with --all: per-tenant domain, {tenant} substituted, e.g. '{tenant}.auth.example.com'")
	cmd.Flags().StringVar(&out, "out", "", "write to this file instead of stdout")
	return cmd
}

func generateCaddyfileOne(tenant, domain, out string) error {
	containerName := "gotrue-" + tenant
	exists, _, err := containerState(containerName)
	if err != nil {
		return err
	}
	if !exists {
		return fmt.Errorf("no such tenant %q", tenant)
	}
	hostPort, err := dockerHostPort(containerName, "9999/tcp")
	if err != nil {
		return fmt.Errorf("reading port for %s (is it running?): %w", containerName, err)
	}
	return writeCaddyfile(out, caddyBlock(tenant, domain, hostPort))
}

func generateCaddyfileAll(domainTemplate, out string) error {
	containers, err := listContainersByLabel(managedByLabel, "role=gotrue")
	if err != nil {
		return err
	}
	if len(containers) == 0 {
		return fmt.Errorf("no tenants to generate config for")
	}
	var blocks []string
	for _, c := range containers {
		tenant := c.label("tenant")
		hostPort := extractHostPort(c.Ports)
		if hostPort == "-" {
			printWarn("skipping %q: not running, no published port to read", tenant)
			continue
		}
		domain := strings.ReplaceAll(domainTemplate, "{tenant}", tenant)
		blocks = append(blocks, caddyBlock(tenant, domain, hostPort))
	}
	if len(blocks) == 0 {
		return fmt.Errorf("no running tenants to generate config for")
	}
	return writeCaddyfile(out, strings.Join(blocks, "\n\n"))
}

func caddyBlock(tenant, domain, hostPort string) string {
	matcher := strings.Join(publicGoTrueRoutes, " ")
	var b strings.Builder
	fmt.Fprintf(&b, "# gotruectl-generated Caddy config for tenant %q — public GoTrue routes\n", tenant)
	fmt.Fprintf(&b, "# only. /admin/* and anything not explicitly listed below default-denies\n")
	fmt.Fprintf(&b, "# (404) rather than being blocklisted: verify the allowed path list\n")
	fmt.Fprintf(&b, "# against your GoTrue version before relying on this in production.\n")
	fmt.Fprintf(&b, "%s {\n", domain)
	fmt.Fprintf(&b, "\tencode gzip\n\n")
	fmt.Fprintf(&b, "\theader {\n")
	fmt.Fprintf(&b, "\t\tStrict-Transport-Security \"max-age=31536000; includeSubDomains; preload\"\n")
	fmt.Fprintf(&b, "\t\tX-Content-Type-Options \"nosniff\"\n")
	fmt.Fprintf(&b, "\t\tX-Frame-Options \"DENY\"\n")
	fmt.Fprintf(&b, "\t\tReferrer-Policy \"strict-origin-when-cross-origin\"\n")
	fmt.Fprintf(&b, "\t\t-Server\n")
	fmt.Fprintf(&b, "\t}\n\n")
	fmt.Fprintf(&b, "\tlog\n\n")
	fmt.Fprintf(&b, "\t@public path %s\n", matcher)
	fmt.Fprintf(&b, "\thandle @public {\n")
	fmt.Fprintf(&b, "\t\treverse_proxy 127.0.0.1:%s\n", hostPort)
	fmt.Fprintf(&b, "\t}\n\n")
	fmt.Fprintf(&b, "\thandle {\n")
	fmt.Fprintf(&b, "\t\trespond \"Not Found\" 404\n")
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "}")
	return b.String()
}

func writeCaddyfile(out, content string) error {
	if out == "" {
		fmt.Println(content)
		return nil
	}
	if err := os.WriteFile(out, []byte(content+"\n"), 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", out, err)
	}
	printSuccess("wrote %s", out)
	printMuted("validate before deploying: caddy validate --config %s --adapter caddyfile", out)
	return nil
}
