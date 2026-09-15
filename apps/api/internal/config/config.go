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
	// ReconcileIntervalSeconds runs the desired-state loop in the API process.
	// 0 disables it. Local dev leaves it off (INFRA_ANALYST.md §15.1: no
	// cluster locally); production enables it (default there is 30).
	ReconcileIntervalSeconds int
	// K8sNamespace is the cluster namespace the k8s driver provisions into.
	K8sNamespace string
	// WorkerImage is the container image the k8s driver launches.
	WorkerImage string
	// ProvisionAutoCreate allows bin-packing to auto-create a worker container.
	ProvisionAutoCreate bool
	// MaxAccountsPerContainer caps accounts per container. The platform unique
	// constraint is the real authority; this bounds total density.
	MaxAccountsPerContainer int
	// ActionBatchParallelism caps how many worker containers act concurrently.
	ActionBatchParallelism int
	// ShutdownTimeoutSeconds is the graceful drain budget.
	ShutdownTimeoutSeconds int

	// JWTSecret signs access tokens. Required when the DB is configured.
	JWTSecret string
	// JWTIssuer is the `iss` claim; stable per deployment.
	JWTIssuer string
	// SecureCookies sets the Secure flag on auth cookies (true behind HTTPS).
	SecureCookies bool
	// CredentialKeyBase64 is the AES-256 key (base64) for credential encryption
	// at rest. Required when the DB is configured.
	CredentialKeyBase64 string
}

// Load reads configuration from the environment, applying safe defaults.
func Load() (Config, error) {
	cfg := Config{
		HTTPAddress:              env("API_ADDR", ":8080"),
		LogLevel:                 env("LOG_LEVEL", "info"),
		DatabaseURL:              os.Getenv("DATABASE_URL"),
		ProvisionerMode:          env("PROVISIONER_MODE", "static"),
		ReconcileIntervalSeconds: envInt("RECONCILE_INTERVAL_SECONDS", 0),
		K8sNamespace:             env("K8S_NAMESPACE", "smm"),
		WorkerImage:              env("WORKER_IMAGE", ""),
		ProvisionAutoCreate:      envBool("PROVISION_AUTO_CREATE", true),
		MaxAccountsPerContainer:  envInt("MAX_ACCOUNTS_PER_CONTAINER", 7),
		ActionBatchParallelism:   envInt("ACTION_BATCH_PARALLELISM", 2),
		ShutdownTimeoutSeconds:   envInt("SHUTDOWN_TIMEOUT_SECONDS", 10),
		JWTSecret:                os.Getenv("JWT_SECRET"),
		JWTIssuer:                env("JWT_ISSUER", "smm-api"),
		SecureCookies:            envBool("SECURE_COOKIES", false),
		CredentialKeyBase64:      os.Getenv("CREDENTIAL_KEY"),
	}

	if cfg.ProvisionerMode != "static" && cfg.ProvisionerMode != "k8s" {
		return Config{}, fmt.Errorf("config: PROVISIONER_MODE must be static|k8s, got %q", cfg.ProvisionerMode)
	}
	if cfg.ReconcileIntervalSeconds < 0 {
		return Config{}, fmt.Errorf("config: RECONCILE_INTERVAL_SECONDS must be >= 0, got %d", cfg.ReconcileIntervalSeconds)
	}
	if cfg.ActionBatchParallelism < 1 {
		return Config{}, fmt.Errorf("config: ACTION_BATCH_PARALLELISM must be >= 1, got %d", cfg.ActionBatchParallelism)
	}
	if cfg.MaxAccountsPerContainer < 1 {
		return Config{}, fmt.Errorf("config: MAX_ACCOUNTS_PER_CONTAINER must be >= 1, got %d", cfg.MaxAccountsPerContainer)
	}
	// The JWT secret is only meaningful once the API talks to the DB (auth on).
	if cfg.DatabaseURL != "" && len(cfg.JWTSecret) < 16 {
		return Config{}, fmt.Errorf("config: JWT_SECRET must be set (>= 16 bytes) when DATABASE_URL is configured")
	}
	// Credential encryption is mandatory whenever accounts can be stored.
	if cfg.DatabaseURL != "" && cfg.CredentialKeyBase64 == "" {
		return Config{}, fmt.Errorf("config: CREDENTIAL_KEY must be set (base64 32-byte key) when DATABASE_URL is configured")
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
