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
