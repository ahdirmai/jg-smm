// Package config loads runtime configuration from the environment. It does not
// read secrets from files; secrets are injected as env vars (or a mounted file
// for dev, see DEVELOPMENT_RULE §8).
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Config is the process-wide configuration.
type Config struct {
	// HTTPAddress is the listen address, e.g. ":8080".
	HTTPAddress string
	// LogLevel is debug|info|warn|error.
	LogLevel string
	// DatabaseURL is a Postgres DSN. Empty disables the DB probe.
	DatabaseURL string

	// ProvisionerMode is static (local bookkeeping, no container is spawned),
	// docker (local workstation: the API itself launches worker containers through
	// the mounted docker socket), or k8s (production).
	ProvisionerMode string
	// ReconcileIntervalSeconds runs the desired-state loop in the API process.
	// 0 disables it. Local dev leaves it off (../../../../docs/INFRA_ANALYST.md §15.1: no
	// cluster locally); production enables it (default there is 30).
	ReconcileIntervalSeconds int
	// K8sNamespace is the cluster namespace the k8s driver provisions into.
	K8sNamespace string
	// WorkerImage is the container image the k8s + docker drivers launch.
	WorkerImage string
	// DockerNetwork is the compose network the docker driver joins workers to so
	// they can reach the API/Redis/MinIO service names. Empty = "smm_default".
	DockerNetwork string
	// DockerSocket is the daemon endpoint the local docker driver dials.
	DockerSocket string
	// DockerPublicHost is the host a browser resolves for the live view; it
	// becomes the NOVNC_URL the worker heartbeats back.
	DockerPublicHost string
	// NovncPortMin/Max bound the live-view host ports the docker driver
	// publishes on 127.0.0.1.
	NovncPortMin int
	NovncPortMax int
	// WorkerPlatforms is the comma-separated platform list handed to every
	// worker container as PLATFORMS.
	WorkerPlatforms string
	// ActionDryRun keeps workers from committing real actions; the docker
	// driver injects it into every container it launches.
	ActionDryRun bool
	// ProvisionAutoCreate allows bin-packing to auto-create a worker container.
	ProvisionAutoCreate bool
	// MaxAccountsPerContainer caps accounts per container. The platform unique
	// constraint is the real authority; this bounds total density.
	MaxAccountsPerContainer int
	// ActionBatchParallelism caps how many worker containers act concurrently.
	ActionBatchParallelism int
	// ActionIntervalSeconds runs the action scheduler loop in the API process
	// (P3-07): it claims due action jobs, enforces the cooldown + rate-limit
	// gates, and publishes survivors to the worker queues. 0 disables it (local
	// dev); production enables it.
	ActionIntervalSeconds int
	// RedisURL is the redis:// URL for the worker transport (action queues,
	// control channel), cooldown gate and rate limiter. Empty disables those.
	RedisURL string
	// ActionCooldownSeconds is the per-(account, target) cooldown gate (P3-09):
	// the same account may not act on the same target twice inside this window.
	// The ticket's contract is 60s; 0 disables the gate only for local play.
	ActionCooldownSeconds int
	// ActionAccountPaceSeconds is the minimum spacing between ANY two actions on
	// the same account, regardless of target or action type (anti-detection).
	// The per-target cooldown does not bound a like+comment+reply burst on one
	// account within minutes — the pattern that gets a fresh account flagged. A
	// real deployment spaces actions out (default 480s / 8 min); 0 disables it.
	ActionAccountPaceSeconds int
	// ActionRateLimits is the per-platform hourly action budget (P3-10) the BE
	// enforces before publishing. A platform absent from the map or set to 0 is
	// treated as disabled (no budget), never unlimited: the defaults are the
	// ticket's safety contract (IG 30/hr, Threads 15/hr), and a missing entry
	// must not silently mean "unlimited".
	ActionRateLimits map[string]int
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
	// CORSAllowedOrigins is the exact list of web origins the browser may call
	// the API from. The dashboard is served on a different port than the API, so
	// every request is cross-origin and needs an explicit allow entry. Empty
	// means "unset"; Load fills the local-dev default.
	CORSAllowedOrigins []string

	// SSEBuffer is the per-subscriber event queue. A slow dashboard that
	// overflows it is disconnected (its EventSource reconnects) rather than
	// blocking the API.
	SSEBuffer int

	// ScrapeIntervalSeconds runs the FIFO scrape scheduler in the API process.
	// 0 disables it (local dev); production enables it.
	ScrapeIntervalSeconds int
	// ScrapeJitterMin/MaxSeconds is the per-account randomized delay the
	// scheduler adds so a fleet of accounts never looks like a bot burst
	// (TICKETS P2-02 AC: jitter 5-15s).
	ScrapeJitterMinSeconds int
	ScrapeJitterMaxSeconds int
	// ScrapeMaxAttempts caps retries on a rate-limited scrape job before it
	// fails permanently (P2-02 backoff).
	ScrapeMaxAttempts int

	// Apify base URL + token for the scrape actor runner (P2-03). The token is
	// a secret injected via env; it is never logged.
	ApifyBaseURL string
	ApifyToken   string
	// ApifyActorPrefix selects the actor id per platform, e.g.
	// "~smm/instagram-scraper". The concrete ids live in PLATFORM_MATRIX.
	ApifyActorPrefix string
	// ApifyBudgetUSD is the monthly Apify spend ceiling (P5-08). 0 publishes no
	// ceiling, which disables the budget alert rather than making it noisy.
	ApifyBudgetUSD float64
	// ProxyBudgetGB is the monthly residential-proxy egress ceiling (P5-08).
	// 0 publishes no ceiling, same semantics.
	ProxyBudgetGB float64

	// LLM (OpenAI-compatible) for AI comment generation. An empty base URL,
	// key or model disables the generator: the endpoint returns 503 and the
	// dashboard hides the button, rather than firing requests with blank
	// credentials.
	LLMBaseURL string
	LLMModel   string
	LLMAPIKey  string

	// AnalyticsIngestIntervalSeconds runs the official-account ingest cron
	// (P2-12). 0 disables it.
	AnalyticsIngestIntervalSeconds int
	// AlertIntervalSeconds runs the monitoring rule engine (P2-07). 0 disables.
	AlertIntervalSeconds int
	// AggregateIntervalSeconds runs the top-post metric re-sampler (P2-05).
	// 0 disables it; the ticket's cadence is 30 minutes (1800s).
	AggregateIntervalSeconds int
	// AnalyticsProvider selects which 3rd-party provider adapter the ingestor
	// uses (PRD F5; provider-agnostic by design).
	AnalyticsProvider string
	// AnalyticsProviderBaseURL + key for the chosen provider.
	AnalyticsProviderBaseURL string
	AnalyticsProviderKey     string

	// MinIO / S3 for raw scrape payloads (P2-04). The bucket is created on first
	// use, so these can point at a fresh MinIO.
	MinioEndpoint string
	MinioUser     string
	MinioPassword string
	MinioBucket   string
	MinioUseSSL   bool

	// SMTP for the weekly report digest (P4-06). The mailer is only constructed
	// when ReportEmailIntervalSeconds > 0, so a local `make up` never needs a
	// mail server. The ticket's cadence is weekly (604800s).
	SmtpAddr                   string
	SmtpHost                   string
	SmtpFrom                   string
	SmtpUsername               string
	SmtpPassword               string
	ReportEmailIntervalSeconds int
	ReportEmailRecipients      []string
	ReportEmailWindowDays      int
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
		DockerNetwork:            env("DOCKER_NETWORK", "smm_default"),
		DockerSocket:             env("DOCKER_SOCKET", "/var/run/docker.sock"),
		DockerPublicHost:         env("DOCKER_PUBLIC_HOST", "localhost"),
		NovncPortMin:             envInt("NOVNC_PORT_MIN", 24100),
		NovncPortMax:             envInt("NOVNC_PORT_MAX", 24299),
		WorkerPlatforms:          env("WORKER_PLATFORMS", "instagram,threads"),
		ActionDryRun:             envBool("ACTION_DRY_RUN", true),
		ProvisionAutoCreate:      envBool("PROVISION_AUTO_CREATE", true),
		MaxAccountsPerContainer:  envInt("MAX_ACCOUNTS_PER_CONTAINER", 7),
		ActionBatchParallelism:   envInt("ACTION_BATCH_PARALLELISM", 2),
		ActionIntervalSeconds:    envInt("ACTION_INTERVAL_SECONDS", 0),
		RedisURL:                 os.Getenv("REDIS_URL"),
		ActionCooldownSeconds:    envInt("ACTION_COOLDOWN_SECONDS", 60),
		ActionAccountPaceSeconds: envInt("ACTION_ACCOUNT_PACE_SECONDS", 480),
		ActionRateLimits: map[string]int{
			"instagram": envInt("ACTION_RATE_LIMIT_INSTAGRAM", 30),
			"threads":   envInt("ACTION_RATE_LIMIT_THREADS", 15),
		},
		ShutdownTimeoutSeconds: envInt("SHUTDOWN_TIMEOUT_SECONDS", 10),
		JWTSecret:              os.Getenv("JWT_SECRET"),
		JWTIssuer:              env("JWT_ISSUER", "smm-api"),
		SecureCookies:          envBool("SECURE_COOKIES", false),
		CredentialKeyBase64:    os.Getenv("CREDENTIAL_KEY"),
		SSEBuffer:              envInt("SSE_BUFFER", 64),
		// The dashboard runs on :24081 against the API on :24080, so the browser's
		// origin is never the API's. Allow that one origin out of the box;
		// any real deployment sets CORS_ALLOWED_ORIGINS explicitly.
		CORSAllowedOrigins: splitList(env("CORS_ALLOWED_ORIGINS", "http://localhost:24081")),

		ScrapeIntervalSeconds:  envInt("SCRAPE_INTERVAL_SECONDS", 0),
		ScrapeJitterMinSeconds: envInt("SCRAPE_JITTER_MIN_SECONDS", 5),
		ScrapeJitterMaxSeconds: envInt("SCRAPE_JITTER_MAX_SECONDS", 15),
		ScrapeMaxAttempts:      envInt("SCRAPE_MAX_ATTEMPTS", 3),

		ApifyBaseURL:     env("APIFY_BASE_URL", "https://api.apify.com/v2"),
		ApifyToken:       os.Getenv("APIFY_TOKEN"),
		ApifyActorPrefix: env("APIFY_ACTOR_PREFIX", "~smm"),
		ApifyBudgetUSD:   envFloat("APIFY_BUDGET_USD", 0),
		ProxyBudgetGB:    envFloat("PROXY_BUDGET_GB", 0),

		LLMBaseURL: env("LLM_BASE_URL", ""),
		LLMModel:   env("LLM_MODEL", ""),
		LLMAPIKey:  os.Getenv("LLM_API_KEY"),

		AnalyticsIngestIntervalSeconds: envInt("ANALYTICS_INGEST_INTERVAL_SECONDS", 0),
		AlertIntervalSeconds:           envInt("ALERT_INTERVAL_SECONDS", 0),
		AggregateIntervalSeconds:       envInt("AGGREGATE_INTERVAL_SECONDS", 0),
		AnalyticsProvider:              env("ANALYTICS_PROVIDER", "thirdparty_a"),
		AnalyticsProviderBaseURL:       env("ANALYTICS_PROVIDER_BASE_URL", "https://provider.example.com/v1"),
		AnalyticsProviderKey:           os.Getenv("ANALYTICS_PROVIDER_KEY"),

		MinioEndpoint: env("MINIO_ENDPOINT", "minio:9000"),
		MinioUser:     env("MINIO_USER", "smm"),
		MinioPassword: os.Getenv("MINIO_PASSWORD"),
		MinioBucket:   env("MINIO_BUCKET", "smm-raw"),
		MinioUseSSL:   envBool("MINIO_USE_SSL", false),

		SmtpAddr:                   env("SMTP_ADDR", ""),
		SmtpHost:                   env("SMTP_HOST", ""),
		SmtpFrom:                   env("SMTP_FROM", "reports@smm.local"),
		SmtpUsername:               os.Getenv("SMTP_USERNAME"),
		SmtpPassword:               os.Getenv("SMTP_PASSWORD"),
		ReportEmailIntervalSeconds: envInt("REPORT_EMAIL_INTERVAL_SECONDS", 0),
		ReportEmailRecipients:      splitList(os.Getenv("REPORT_EMAIL_TO")),
		ReportEmailWindowDays:      envInt("REPORT_EMAIL_WINDOW_DAYS", 7),
	}

	if cfg.ProvisionerMode != "static" && cfg.ProvisionerMode != "docker" && cfg.ProvisionerMode != "k8s" {
		return Config{}, fmt.Errorf("config: PROVISIONER_MODE must be static|docker|k8s, got %q", cfg.ProvisionerMode)
	}
	if cfg.ReconcileIntervalSeconds < 0 {
		return Config{}, fmt.Errorf("config: RECONCILE_INTERVAL_SECONDS must be >= 0, got %d", cfg.ReconcileIntervalSeconds)
	}
	if cfg.NovncPortMin <= 0 || cfg.NovncPortMax < cfg.NovncPortMin {
		return Config{}, fmt.Errorf("config: NOVNC_PORT_MIN must be <= NOVNC_PORT_MAX and > 0, got %d-%d", cfg.NovncPortMin, cfg.NovncPortMax)
	}
	if cfg.ActionBatchParallelism < 1 {
		return Config{}, fmt.Errorf("config: ACTION_BATCH_PARALLELISM must be >= 1, got %d", cfg.ActionBatchParallelism)
	}
	if cfg.MaxAccountsPerContainer < 1 {
		return Config{}, fmt.Errorf("config: MAX_ACCOUNTS_PER_CONTAINER must be >= 1, got %d", cfg.MaxAccountsPerContainer)
	}
	if cfg.ScrapeIntervalSeconds < 0 {
		return Config{}, fmt.Errorf("config: SCRAPE_INTERVAL_SECONDS must be >= 0, got %d", cfg.ScrapeIntervalSeconds)
	}
	if cfg.ScrapeJitterMinSeconds < 0 || cfg.ScrapeJitterMaxSeconds < cfg.ScrapeJitterMinSeconds {
		return Config{}, fmt.Errorf("config: SCRAPE_JITTER must satisfy 0 <= MIN <= MAX, got min=%d max=%d", cfg.ScrapeJitterMinSeconds, cfg.ScrapeJitterMaxSeconds)
	}
	if cfg.ScrapeMaxAttempts < 1 {
		return Config{}, fmt.Errorf("config: SCRAPE_MAX_ATTEMPTS must be >= 1, got %d", cfg.ScrapeMaxAttempts)
	}
	if cfg.AnalyticsIngestIntervalSeconds < 0 {
		return Config{}, fmt.Errorf("config: ANALYTICS_INGEST_INTERVAL_SECONDS must be >= 0, got %d", cfg.AnalyticsIngestIntervalSeconds)
	}
	if cfg.AlertIntervalSeconds < 0 {
		return Config{}, fmt.Errorf("config: ALERT_INTERVAL_SECONDS must be >= 0, got %d", cfg.AlertIntervalSeconds)
	}
	if cfg.AggregateIntervalSeconds < 0 {
		return Config{}, fmt.Errorf("config: AGGREGATE_INTERVAL_SECONDS must be >= 0, got %d", cfg.AggregateIntervalSeconds)
	}
	if cfg.ActionIntervalSeconds < 0 {
		return Config{}, fmt.Errorf("config: ACTION_INTERVAL_SECONDS must be >= 0, got %d", cfg.ActionIntervalSeconds)
	}
	if cfg.ActionCooldownSeconds < 0 {
		return Config{}, fmt.Errorf("config: ACTION_COOLDOWN_SECONDS must be >= 0, got %d", cfg.ActionCooldownSeconds)
	}
	// The report digest is opt-in. When it runs it needs a mail server and at
	// least one recipient; an empty list would silently tick and no-op.
	if cfg.ReportEmailIntervalSeconds > 0 {
		if cfg.SmtpAddr == "" {
			return Config{}, fmt.Errorf("config: SMTP_ADDR is required when REPORT_EMAIL_INTERVAL_SECONDS > 0")
		}
		if len(cfg.ReportEmailRecipients) == 0 {
			return Config{}, fmt.Errorf("config: REPORT_EMAIL_TO is required when REPORT_EMAIL_INTERVAL_SECONDS > 0")
		}
	}
	if cfg.ReportEmailWindowDays < 0 {
		return Config{}, fmt.Errorf("config: REPORT_EMAIL_WINDOW_DAYS must be >= 0, got %d", cfg.ReportEmailWindowDays)
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

// envFloat parses a float env var. Used for budget ceilings, which are dollars
// and gigabytes, not counts.
func envFloat(key string, def float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return def
}

// splitList parses a comma-separated env var into a trimmed, de-blanked slice.
// An empty var yields nil, which the consumer treats as "not configured".
func splitList(v string) []string {
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
