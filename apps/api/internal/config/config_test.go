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
