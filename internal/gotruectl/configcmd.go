package gotruectl

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "View or set gotruectl's persisted config (~/.gotrue-builder/config.yaml by default)",
	}
	cmd.AddCommand(newConfigShowCmd())
	cmd.AddCommand(newConfigSetSMTPCmd())
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the fully resolved config (defaults + file + env)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			fmt.Printf("config file:        %s\n", configPath())
			fmt.Printf("postgres_image:     %s\n", cfg.PostgresImage)
			fmt.Printf("network:            %s\n", cfg.Network)
			fmt.Printf("volume:             %s\n", cfg.Volume)
			fmt.Printf("default_site_url:   %s\n", cfg.DefaultSiteURL)
			fmt.Printf("default_jwt_aud:    %s\n", cfg.DefaultJWTAud)
			fmt.Printf("backup_dir:         %s\n", cfg.BackupDir)
			fmt.Printf("smtp_host:          %s\n", cfg.SMTPHost)
			fmt.Printf("smtp_port:          %s\n", cfg.SMTPPort)
			fmt.Printf("smtp_user:          %s\n", cfg.SMTPUser)
			fmt.Printf("smtp_pass:          %s\n", maskSecret(cfg.SMTPPass))
			fmt.Printf("smtp_admin_email:   %s\n", cfg.SMTPAdminEmail)
			fmt.Printf("smtp_sender_name:   %s\n", cfg.SMTPSenderName)
			return nil
		},
	}
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	return "(set)"
}

func newConfigSetSMTPCmd() *cobra.Command {
	var host, port, user, pass, adminEmail, senderName string
	cmd := &cobra.Command{
		Use:   "set-smtp",
		Short: "Configure the SMTP server new tenants use for magic-link/OTP emails",
		Long: `Persist SMTP settings to the config file. Every subsequent "tenant create"
picks these up automatically (unless overridden with that command's
--smtp-* flags). Any flag left unset here is prompted for interactively,
showing the current value (from the config file, or the built-in default)
so pressing enter keeps it unchanged.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig()
			if err != nil {
				return err
			}
			f := cmd.Flags()

			resolve := func(flagName, current, value string) string {
				if f.Changed(flagName) {
					return value
				}
				return promptString(flagName, current)
			}

			host = resolve("host", cfg.SMTPHost, host)
			port = resolve("port", cfg.SMTPPort, port)
			user = resolve("user", cfg.SMTPUser, user)
			if !f.Changed("pass") {
				shown := ""
				if cfg.SMTPPass != "" {
					shown = "(unchanged)"
				}
				entered := promptString("pass", shown)
				if entered != "(unchanged)" {
					pass = entered
				} else {
					pass = cfg.SMTPPass
				}
			}
			adminEmail = resolve("admin-email", cfg.SMTPAdminEmail, adminEmail)
			senderName = resolve("sender-name", cfg.SMTPSenderName, senderName)

			return saveSMTPConfig(host, port, user, pass, adminEmail, senderName)
		},
	}
	cmd.Flags().StringVar(&host, "host", "", "SMTP server host")
	cmd.Flags().StringVar(&port, "port", "", "SMTP server port")
	cmd.Flags().StringVar(&user, "user", "", "SMTP username")
	cmd.Flags().StringVar(&pass, "pass", "", "SMTP password")
	cmd.Flags().StringVar(&adminEmail, "admin-email", "", "From-address for outgoing mail")
	cmd.Flags().StringVar(&senderName, "sender-name", "", "From-name for outgoing mail")
	return cmd
}

func saveSMTPConfig(host, port, user, pass, adminEmail, senderName string) error {
	v, err := loadViper()
	if err != nil {
		return err
	}
	v.Set("smtp_host", host)
	v.Set("smtp_port", port)
	v.Set("smtp_user", user)
	v.Set("smtp_pass", pass)
	v.Set("smtp_admin_email", adminEmail)
	v.Set("smtp_sender_name", senderName)

	path := configPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := v.WriteConfigAs(path); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	_ = os.Chmod(path, 0o600) // holds the SMTP password in plaintext
	fmt.Println("SMTP config saved to", path)
	return nil
}
