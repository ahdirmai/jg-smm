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
