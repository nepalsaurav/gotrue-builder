package gotruectl

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/spf13/cobra"
)

func newKeyCmd() *cobra.Command {
	var tenant string
	var ttl time.Duration
	cmd := &cobra.Command{
		Use:   "key",
		Short: "Mint a service_role JWT for a tenant, signed with its own GOTRUE_JWT_SECRET",
		Long: `Mint a service_role JWT for a tenant, for calling that tenant's GoTrue
Admin API (/admin/*) directly with curl or another tool. gotruectl's own
"admin" command mints and uses one of these automatically, so you only need
this command when you want the raw token yourself.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if tenant == "" {
				return fmt.Errorf("--tenant is required")
			}
			token, err := mintServiceRoleJWT(tenant, ttl)
			if err != nil {
				return err
			}
			fmt.Println(token)
			return nil
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant name")
	cmd.Flags().DurationVar(&ttl, "ttl", time.Hour, "token lifetime")
	return cmd
}

// readTenantJWTSecret reads GOTRUE_JWT_SECRET back out of the tenant's own
// .env file — the same secret its GoTrue container was started with, so a
// token minted here verifies against that instance without any extra state.
func readTenantJWTSecret(tenant string) (string, error) {
	envPath, err := tenantEnvPath(tenant)
	if err != nil {
		return "", err
	}
	env, err := parseEnvFile(envPath)
	if err != nil {
		return "", fmt.Errorf("reading tenant %q's env file: %w (has it been created with `tenant create`?)", tenant, err)
	}
	secret, ok := env["GOTRUE_JWT_SECRET"]
	if !ok {
		return "", fmt.Errorf("GOTRUE_JWT_SECRET not found in tenant %q's env file", tenant)
	}
	return secret, nil
}

// mintServiceRoleJWT builds an HS256 JWT with role=service_role, which
// GoTrue's Admin API accepts by default (its JWT.AdminRoles config includes
// service_role out of the box).
func mintServiceRoleJWT(tenant string, ttl time.Duration) (string, error) {
	secret, err := readTenantJWTSecret(tenant)
	if err != nil {
		return "", err
	}
	now := time.Now()
	claims := jwt.MapClaims{
		"role": "service_role",
		"iss":  "gotruectl",
		"iat":  now.Unix(),
		"exp":  now.Add(ttl).Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString([]byte(secret))
	if err != nil {
		return "", fmt.Errorf("signing token: %w", err)
	}
	return signed, nil
}
