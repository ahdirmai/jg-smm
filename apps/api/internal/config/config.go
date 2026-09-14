// Package config loads runtime configuration from the environment. It does not
// read secrets from files; secrets are injected as env vars (or a mounted file
// for dev, see DEVELOPMENT_RULE §8).
package config

import (
	"fmt"
	"os"
	"strconv"
)

// Config is the process-wide configuration.
type Config struct {
	// HTTPAddress is the listen address, e.g. ":8080".
	HTTPAddress string
	// LogLevel is debug|info|warn|error.
	LogLevel string
	// DatabaseURL is a Postgres DSN. Empty disables the DB probe.
	DatabaseURL string

	// ProvisionerMode is static (local, no Kubernetes) or k8s (production).
	ProvisionerMode string
	// ProvisionAutoCreate allows bin-packing to auto-create a worker container.
	ProvisionAutoCreate bool
	// ActionBatchParallelism caps how many worker containers act concurrently.
	ActionBatchParallelism int
	// ShutdownTimeoutSeconds is the graceful drain budget.
	ShutdownTimeoutSeconds int
}

// Load reads configuration from the environment, applying safe defaults.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddress:            env("API_ADDR", ":8080"),
		LogLevel:               env("LOG_LEVEL", "info"),
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		ProvisionerMode:        env("PROVISIONER_MODE", "static"),
		ProvisionAutoCreate:    envBool("PROVISION_AUTO_CREATE", true),
		ActionBatchParallelism: envInt("ACTION_BATCH_PARALLELISM", 2),
		ShutdownTimeoutSeconds: envInt("SHUTDOWN_TIMEOUT_SECONDS", 10),
	}

	if cfg.ProvisionerMode != "static" && cfg.ProvisionerMode != "k8s" {
		return Config{}, fmt.Errorf("config: PROVISIONER_MODE must be static|k8s, got %q", cfg.ProvisionerMode)
	}
	if cfg.ActionBatchParallelism < 1 {
		return Config{}, fmt.Errorf("config: ACTION_BATCH_PARALLELISM must be >= 1, got %d", cfg.ActionBatchParallelism)
	}
	return cfg, nil
}

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func envBool(key string, def bool) bool {
	if v := os.Getenv(key); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return def
}
