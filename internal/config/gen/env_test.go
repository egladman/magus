package gen_test

import (
	"os"
	"testing"

	"github.com/egladman/magus/internal/config"
	configgen "github.com/egladman/magus/internal/config/gen"
)

func TestApplyEnv_FlakeEnabledTrue(t *testing.T) {
	t.Setenv("MAGUS_FLAKE_ENABLED", "true")
	cfg := config.Defaults()
	configgen.ApplyEnv(&cfg, os.Getenv)
	if !cfg.Flake.Enabled {
		t.Error("MAGUS_FLAKE_ENABLED=true: Flake.Enabled = false, want true")
	}
}

func TestApplyEnv_FlakeEnabledFalse(t *testing.T) {
	t.Setenv("MAGUS_FLAKE_ENABLED", "false")
	cfg := config.Defaults()
	configgen.ApplyEnv(&cfg, os.Getenv)
	if cfg.Flake.Enabled {
		t.Error("MAGUS_FLAKE_ENABLED=false: Flake.Enabled = true, want false")
	}
}

func TestApplyEnvToConfig(t *testing.T) {
	t.Setenv("MAGUS_CACHE_IMMUTABLE", "true")
	t.Setenv("MAGUS_CONCURRENCY", "6")
	t.Setenv("MAGUS_DRY_RUN", "1")

	cfg := config.Defaults()
	configgen.ApplyEnv(&cfg, os.Getenv)

	if !cfg.Cache.Immutable {
		t.Errorf("Cache.Immutable = %v, want true", cfg.Cache.Immutable)
	}
	if cfg.Concurrency != 6 {
		t.Errorf("Concurrency = %d, want 6", cfg.Concurrency)
	}
	if !cfg.DryRun {
		t.Error("DryRun should be true")
	}
}

func TestApplyEnv_SandboxEnabled(t *testing.T) {
	t.Setenv("MAGUS_SANDBOX_ENABLED", "true")
	cfg := config.Defaults()
	configgen.ApplyEnv(&cfg, os.Getenv)
	if !cfg.Sandbox.Enabled {
		t.Error("MAGUS_SANDBOX_ENABLED=true: Sandbox.Enabled = false, want true")
	}
}
