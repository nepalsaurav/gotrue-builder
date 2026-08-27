package gotruectl

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"
)

func newAdminCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "admin",
		Short: "Call a tenant's GoTrue Admin API (/admin/*)",
	}
	cmd.AddCommand(newAdminCreateUserCmd())
	cmd.AddCommand(newAdminListUsersCmd())
	return cmd
}

func newAdminCreateUserCmd() *cobra.Command {
	var tenant, email, password string
	var emailConfirm bool
	cmd := &cobra.Command{
		Use:   "create-user",
		Short: "Create a user directly via the Admin API (bypasses public signup)",
		Long: `Create a user directly via a tenant's GoTrue Admin API. Useful for
provisioning accounts on a tenant that has public signup disabled (e.g. an
admin/staff pool) — the same gap abc_project_app's own admin-user endpoint
has today: it creates a local DB record but never a matching GoTrue identity.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if tenant == "" || email == "" {
				return fmt.Errorf("--tenant and --email are required")
			}
			body := map[string]any{
				"email":         email,
				"email_confirm": emailConfirm,
			}
			if password != "" {
				body["password"] = password
			}
			resp, err := adminRequest(tenant, http.MethodPost, "/admin/users", body)
			if err != nil {
				return err
			}
			return printJSON(resp)
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant name")
	cmd.Flags().StringVar(&email, "email", "", "new user's email")
	cmd.Flags().StringVar(&password, "password", "", "new user's password (omit to require magic-link/OTP login instead)")
	cmd.Flags().BoolVar(&emailConfirm, "email-confirm", true, "mark the email pre-confirmed so the user can log in immediately")
	return cmd
}

func newAdminListUsersCmd() *cobra.Command {
	var tenant string
	var page, perPage int
	cmd := &cobra.Command{
		Use:   "list-users",
		Short: "List users on a tenant via the Admin API",
		RunE: func(cmd *cobra.Command, args []string) error {
			if tenant == "" {
				return fmt.Errorf("--tenant is required")
			}
			path := fmt.Sprintf("/admin/users?page=%d&per_page=%d", page, perPage)
			resp, err := adminRequest(tenant, http.MethodGet, path, nil)
			if err != nil {
				return err
			}
			return printJSON(resp)
		},
	}
	cmd.Flags().StringVar(&tenant, "tenant", "", "tenant name")
	cmd.Flags().IntVar(&page, "page", 1, "page number")
	cmd.Flags().IntVar(&perPage, "per-page", 50, "results per page")
	return cmd
}

// adminRequest mints a fresh short-lived service_role token for the tenant
// and calls its Admin API. The token is never persisted — it's generated
// per call and only lives for the duration of the request.
func adminRequest(tenant, method, path string, body any) (json.RawMessage, error) {
	containerName := "gotrue-" + tenant
	_, running, err := containerState(containerName)
	if err != nil {
		return nil, err
	}
	if !running {
		return nil, fmt.Errorf("tenant %q is not running (container %s) — `tenant start --name %s` first", tenant, containerName, tenant)
	}

	hostPort, err := dockerHostPort(containerName, "9999/tcp")
	if err != nil {
		return nil, err
	}
	baseURL := "http://localhost:" + hostPort

	token, err := mintServiceRoleJWT(tenant, 2*time.Minute)
	if err != nil {
		return nil, err
	}

	var reqBody io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encoding request body: %w", err)
		}
		reqBody = bytes.NewReader(encoded)
	}

	req, err := http.NewRequest(method, baseURL+path, reqBody)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("calling %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, string(respBody))
	}
	return respBody, nil
}

func printJSON(raw json.RawMessage) error {
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, raw, "", "  "); err != nil {
		// Not JSON (e.g. empty body) — print as-is rather than failing.
		fmt.Println(string(raw))
		return nil
	}
	fmt.Println(pretty.String())
	return nil
}
