package config

import (
	"errors"
	"os"
)

// Config holds all runtime configuration for the portal.
type Config struct {
	Port          string // HTTP listen port, default "3000"
	DBPath        string // SQLite file path, default "data/portal.db"
	EncryptionKey string // 32-byte hex key for AES-256-GCM field encryption
	SessionSecret string // secret used to sign/verify session tokens
	AppEnv        string // "development" | "production"
	BannedWords   string // comma-separated list of prohibited comment words
}

// Load reads configuration from environment variables, applying defaults where
// a variable is absent or empty.
func Load() *Config {
	return &Config{
		Port:          envOrDefault("PORT", "3000"),
		DBPath:        envOrDefault("DB_PATH", "data/portal.db"),
		EncryptionKey: envOrDefault("ENCRYPTION_KEY", ""),
		SessionSecret: envOrDefault("SESSION_SECRET", ""),
		AppEnv:        envOrDefault("APP_ENV", "development"),
		BannedWords:   envOrDefault("BANNED_WORDS", ""),
	}
}

// Validate returns an error if any required secret configuration is absent.
// Call this at startup so the application fails fast rather than running with
// an empty encryption key or session secret.
func (c *Config) Validate() error {
	var errs []error
	if c.EncryptionKey == "" {
		errs = append(errs, errors.New("ENCRYPTION_KEY must be set"))
	}
	if c.SessionSecret == "" {
		errs = append(errs, errors.New("SESSION_SECRET must be set"))
	}
	return errors.Join(errs...)
}

// envOrDefault returns the value of the named environment variable, or
// fallback when the variable is unset or empty.
func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
