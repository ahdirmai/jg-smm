package config

import "testing"

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("API_ADDR", "")
	t.Setenv("LOG_LEVEL", "")
	t.Setenv("DATABASE_URL", "")
	t.Setenv("PROVISIONER_MODE", "")
	t.Setenv("PROVISION_AUTO_CREATE", "")
	t.Setenv("ACTION_BATCH_PARALLELISM", "")
	t.Setenv("SHUTDOWN_TIMEOUT_SECONDS", "")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.HTTPAddress != ":8080" {
		t.Errorf("HTTPAddress: got %q", cfg.HTTPAddress)
	}
	if cfg.ProvisionerMode != "static" {
		t.Errorf("ProvisionerMode: got %q", cfg.ProvisionerMode)
	}
	if !cfg.ProvisionAutoCreate {
		t.Error("ProvisionAutoCreate: want true by default")
	}
}

func TestLoad_InvalidProvisionerMode(t *testing.T) {
	t.Setenv("PROVISIONER_MODE", "bogus")
	if _, err := Load(); err == nil {
		t.Fatal("want error for invalid PROVISIONER_MODE")
	}
}

func TestLoad_InvalidBatchParallelism(t *testing.T) {
	t.Setenv("PROVISIONER_MODE", "")
	t.Setenv("ACTION_BATCH_PARALLELISM", "0")
	if _, err := Load(); err == nil {
		t.Fatal("want error for ACTION_BATCH_PARALLELISM < 1")
	}
}

// TestLoad_ActionRateLimits pins the per-platform hourly budgets (P3-10) to
// the ticket's safety contract: IG 30/jam, Threads 15/jam. These are the
// platform-tolerance numbers the scheduler enforces, so a silent default
// change here would put sessions at risk.
func TestLoad_ActionRateLimits(t *testing.T) {
	t.Setenv("PROVISIONER_MODE", "")
	t.Setenv("ACTION_RATE_LIMIT_INSTAGRAM", "")
	t.Setenv("ACTION_RATE_LIMIT_THREADS", "")
	t.Setenv("ACTION_RATE_LIMIT_YOUTUBE", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ActionRateLimits["instagram"] != 30 {
		t.Errorf("instagram limit = %d, want 30", cfg.ActionRateLimits["instagram"])
	}
	if cfg.ActionRateLimits["threads"] != 15 {
		t.Errorf("threads limit = %d, want 15", cfg.ActionRateLimits["threads"])
	}
	if cfg.ActionRateLimits["youtube"] != 10 {
		t.Errorf("youtube limit = %d, want 10", cfg.ActionRateLimits["youtube"])
	}
	if cfg.ActionCooldownSeconds != 60 {
		t.Errorf("cooldown = %d, want 60", cfg.ActionCooldownSeconds)
	}

	// The limits are overridable per platform without touching code.
	t.Setenv("ACTION_RATE_LIMIT_INSTAGRAM", "5")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ActionRateLimits["instagram"] != 5 {
		t.Errorf("overridden instagram limit = %d, want 5", cfg.ActionRateLimits["instagram"])
	}
}

// TestLoad_ActionCooldownInvalid guards the cooldown gate's own bound.
func TestLoad_ActionCooldownInvalid(t *testing.T) {
	t.Setenv("PROVISIONER_MODE", "")
	t.Setenv("ACTION_COOLDOWN_SECONDS", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("want error for negative ACTION_COOLDOWN_SECONDS")
	}
}

func TestLoad_MaxAccountsPerContainer(t *testing.T) {
	t.Setenv("PROVISIONER_MODE", "")
	t.Setenv("MAX_ACCOUNTS_PER_CONTAINER", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxAccountsPerContainer != 7 {
		t.Errorf("default MaxAccountsPerContainer = %d, want 7", cfg.MaxAccountsPerContainer)
	}

	t.Setenv("MAX_ACCOUNTS_PER_CONTAINER", "3")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.MaxAccountsPerContainer != 3 {
		t.Errorf("MaxAccountsPerContainer = %d, want 3", cfg.MaxAccountsPerContainer)
	}

	t.Setenv("MAX_ACCOUNTS_PER_CONTAINER", "0")
	if _, err := Load(); err == nil {
		t.Fatal("want error for MAX_ACCOUNTS_PER_CONTAINER < 1")
	}
}

func TestLoad_ReconcileInterval(t *testing.T) {
	t.Setenv("PROVISIONER_MODE", "")
	t.Setenv("RECONCILE_INTERVAL_SECONDS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ReconcileIntervalSeconds != 0 {
		t.Errorf("default ReconcileIntervalSeconds = %d, want 0 (off)", cfg.ReconcileIntervalSeconds)
	}

	t.Setenv("RECONCILE_INTERVAL_SECONDS", "45")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ReconcileIntervalSeconds != 45 {
		t.Errorf("ReconcileIntervalSeconds = %d, want 45", cfg.ReconcileIntervalSeconds)
	}

	t.Setenv("RECONCILE_INTERVAL_SECONDS", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("want error for RECONCILE_INTERVAL_SECONDS < 0")
	}
}

func TestLoad_RequiresCredentialKeyWithDB(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://x:y@localhost:5432/db")
	t.Setenv("JWT_SECRET", "0123456789abcdef")
	t.Setenv("CREDENTIAL_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("want error when DATABASE_URL is set but CREDENTIAL_KEY is empty")
	}

	t.Setenv("CREDENTIAL_KEY", "v0k2nMVGJAq5Ta69vg36gcxNB4wiClCeOFMC6WBemH8=")
	if _, err := Load(); err != nil {
		t.Fatalf("unexpected error with a key present: %v", err)
	}
}

// TestLoad_ReportEmailOptIn guards the P4-06 contract: the digest may be off
// with no mail server configured, but enabling it without SMTP_ADDR or a
// recipient list is a misconfiguration that must surface at boot.
func TestLoad_ReportEmailOptIn(t *testing.T) {
	t.Setenv("PROVISIONER_MODE", "")
	t.Setenv("DATABASE_URL", "")

	// Off by default: no SMTP and no recipients is fine.
	t.Setenv("REPORT_EMAIL_INTERVAL_SECONDS", "0")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ReportEmailIntervalSeconds != 0 {
		t.Errorf("default interval = %d, want 0", cfg.ReportEmailIntervalSeconds)
	}
	if cfg.ReportEmailWindowDays != 7 {
		t.Errorf("default window = %d, want 7", cfg.ReportEmailWindowDays)
	}
	if cfg.ReportEmailRecipients != nil {
		t.Errorf("default recipients = %v, want nil", cfg.ReportEmailRecipients)
	}

	// Enabled with no SMTP_ADDR must fail at Load, not at first tick.
	t.Setenv("REPORT_EMAIL_INTERVAL_SECONDS", "604800")
	if _, err := Load(); err == nil {
		t.Fatal("want error when REPORT_EMAIL_INTERVAL_SECONDS > 0 but SMTP_ADDR is empty")
	}

	// SMTP present but no recipient is equally broken.
	t.Setenv("SMTP_ADDR", "mailpit:1025")
	if _, err := Load(); err == nil {
		t.Fatal("want error when REPORT_EMAIL_TO is empty")
	}

	// A valid pair loads, and the recipient list is split+trimmed.
	t.Setenv("REPORT_EMAIL_TO", " team@x.com , ops@x.com,")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"team@x.com", "ops@x.com"}
	if len(cfg.ReportEmailRecipients) != len(want) {
		t.Fatalf("recipients = %v, want %v", cfg.ReportEmailRecipients, want)
	}
	for i := range want {
		if cfg.ReportEmailRecipients[i] != want[i] {
			t.Errorf("recipients[%d] = %q, want %q", i, cfg.ReportEmailRecipients[i], want[i])
		}
	}

	// A negative window is not a valid look-back.
	t.Setenv("REPORT_EMAIL_WINDOW_DAYS", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("want error for negative REPORT_EMAIL_WINDOW_DAYS")
	}
}

// TestLoad_ScrapeRateLimit pins the ceiling on the two endpoints that spend
// money. The default is deliberately 0 = no ceiling, so the self-hosted path is
// unchanged; a deployment that wants the guard sets the pair. The failure mode
// this guards is an unset variable silently becoming "refuse everything", which
// is why SetSpendLimit treats <= 0 as unlimited rather than as a budget of zero.
func TestLoad_ScrapeRateLimit(t *testing.T) {
	t.Setenv("PROVISIONER_MODE", "")
	t.Setenv("SCRAPE_RATE_LIMIT", "")
	t.Setenv("SCRAPE_RATE_LIMIT_WINDOW_SECONDS", "")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ScrapeRateLimit != 0 {
		t.Errorf("default SCRAPE_RATE_LIMIT = %d, want 0 (no ceiling)", cfg.ScrapeRateLimit)
	}
	if cfg.ScrapeRateLimitWindowSeconds != 60 {
		t.Errorf("default window = %d, want 60", cfg.ScrapeRateLimitWindowSeconds)
	}

	// Both are env-overridable.
	t.Setenv("SCRAPE_RATE_LIMIT", "20")
	t.Setenv("SCRAPE_RATE_LIMIT_WINDOW_SECONDS", "120")
	cfg, err = Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ScrapeRateLimit != 20 || cfg.ScrapeRateLimitWindowSeconds != 120 {
		t.Errorf("overridden ceiling = %d/%ds, want 20/120s", cfg.ScrapeRateLimit, cfg.ScrapeRateLimitWindowSeconds)
	}
}

// A negative limit or a sub-second window is a config mistake, not a policy.
func TestLoad_ScrapeRateLimitRejectsGarbage(t *testing.T) {
	t.Setenv("PROVISIONER_MODE", "")
	t.Setenv("SCRAPE_RATE_LIMIT", "-1")
	if _, err := Load(); err == nil {
		t.Fatal("want error for negative SCRAPE_RATE_LIMIT")
	}

	t.Setenv("SCRAPE_RATE_LIMIT", "5")
	t.Setenv("SCRAPE_RATE_LIMIT_WINDOW_SECONDS", "0")
	if _, err := Load(); err == nil {
		t.Fatal("want error for SCRAPE_RATE_LIMIT_WINDOW_SECONDS < 1")
	}
}

// Stateless pool mode binds to the secret that authenticates every push: asking
// for it without one is a configuration error, and the default follows the
// secret rather than claiming a pool the API cannot secure.
func TestWorkerPoolModeFollowsSecret(t *testing.T) {
	if got := resolvedPoolMode("", ""); got != PoolModeLegacy {
		t.Fatalf("default without a secret = %q, want %q", got, PoolModeLegacy)
	}
	if got := resolvedPoolMode("", "s3cret"); got != PoolModeStateless {
		t.Fatalf("default with a secret = %q, want %q", got, PoolModeStateless)
	}
	if got := resolvedPoolMode(PoolModeLegacy, "s3cret"); got != PoolModeLegacy {
		t.Fatalf("explicit legacy must win, got %q", got)
	}
	if got := resolvedPoolMode(PoolModeStateless, ""); got != PoolModeStateless {
		t.Fatalf("explicit stateless must be reported truthfully, got %q", got)
	}

	cfg := Config{
		HTTPAddress:                  ":0",
		ProvisionerMode:              "static",
		NovncPortMin:                 1000,
		NovncPortMax:                 2000,
		ActionBatchParallelism:       1,
		MaxAccountsPerContainer:      1,
		WorkerPoolMode:               PoolModeStateless,
		WorkerPoolSize:               10,
		NoVNCTicketTTLSeconds:        300,
		AccountLockTTLSeconds:        300,
		WorkerLeaseTTLSeconds:        660,
		InternalAPISecret:            "s3cret",
		ScrapeMaxAttempts:            1,
		ScrapeRateLimitWindowSeconds: 60,
		SessionRefreshLeadHours:      168,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("a stateless config with a secret must validate: %v", err)
	}
	cfg.WorkerPoolMode = "bogus"
	if err := cfg.Validate(); err == nil {
		t.Fatalf("an unknown pool mode must be rejected")
	}
}
