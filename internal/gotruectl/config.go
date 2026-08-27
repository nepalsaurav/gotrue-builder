package gotruectl

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds the settings worth setting once and reusing across
// invocations. Precedence (highest first): CLI flags, GOTRUCTL_* env vars,
// the config file, these defaults. Per-tenant secrets (JWT secret, DB
// password) are deliberately not here — they're generated per-tenant state
// written to each tenant's own .env file, not shared config.
type Config struct {
	PostgresImage  string `mapstructure:"postgres_image"`
	Network        string `mapstructure:"network"`
	Volume         string `mapstructure:"volume"`
	DefaultSiteURL string `mapstructure:"default_site_url"`
	DefaultJWTAud  string `mapstructure:"default_jwt_aud"`
	BackupDir      string `mapstructure:"backup_dir"`

	// SMTP is the outgoing-mail server new tenants use for magic
	// links/OTP emails, set once via `config set-smtp` and reused by every
	// `tenant create` unless overridden with that command's --smtp-* flags.
	// Left blank by default: a tenant with no SMTP host configured simply
	// has GoTrue's email flows disabled, which is the safe default.
	SMTPHost       string `mapstructure:"smtp_host"`
	SMTPPort       string `mapstructure:"smtp_port"`
	SMTPUser       string `mapstructure:"smtp_user"`
	SMTPPass       string `mapstructure:"smtp_pass"`
	SMTPAdminEmail string `mapstructure:"smtp_admin_email"`
	SMTPSenderName string `mapstructure:"smtp_sender_name"`
}

var cfgFile string // set by the root command's --config flag

func defaultConfigPath() string {
	base, err := baseDir()
	if err != nil {
		return ""
	}
	return filepath.Join(base, "config.yaml")
}

func configPath() string {
	if cfgFile != "" {
		return cfgFile
	}
	return defaultConfigPath()
}

// loadViper sets up the layered config (defaults -> file -> GOTRUCTL_* env)
// and reads the file if present; a missing file is not an error, since the
// file is optional and only created on demand by `config set-smtp`.
func loadViper() (*viper.Viper, error) {
	v := viper.New()

	v.SetDefault("postgres_image", "postgres:15-alpine")
	v.SetDefault("network", "gotrue-net")
	v.SetDefault("volume", "gotrue-postgres-data")
	v.SetDefault("default_site_url", "http://localhost:5173")
	v.SetDefault("default_jwt_aud", "authenticated")
	v.SetDefault("smtp_port", "587")
	base, err := baseDir()
	if err != nil {
		return nil, err
	}
	v.SetDefault("backup_dir", filepath.Join(base, "backups"))

	v.SetEnvPrefix("GOTRUCTL")
	v.AutomaticEnv()

	path := configPath()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok && !os.IsNotExist(err) {
			return nil, fmt.Errorf("reading config file %s: %w", path, err)
		}
	}
	return v, nil
}

// loadConfig is the read-only path most commands use: layered config,
// unmarshaled into a Config. Safe to call once per command run.
func loadConfig() (*Config, error) {
	v, err := loadViper()
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}
	return &cfg, nil
}
