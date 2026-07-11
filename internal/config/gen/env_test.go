package gen

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/egladman/magus/internal/config"
)

func TestApplyEnv_VolatilityEnabledTrue(t *testing.T) {
	t.Setenv("MAGUS_VOLATILITY_ENABLED", "true")
	cfg := config.Defaults()
	ApplyEnv(&cfg, os.Getenv)
	assert.True(t, cfg.Volatility.Enabled, "MAGUS_VOLATILITY_ENABLED=true: Volatility.Enabled should be true")
}

func TestApplyEnv_VolatilityEnabledFalse(t *testing.T) {
	t.Setenv("MAGUS_VOLATILITY_ENABLED", "false")
	cfg := config.Defaults()
	ApplyEnv(&cfg, os.Getenv)
	assert.False(t, cfg.Volatility.Enabled, "MAGUS_VOLATILITY_ENABLED=false: Volatility.Enabled should be false")
}

func TestApplyEnvToConfig(t *testing.T) {
	t.Setenv("MAGUS_CACHE_IMMUTABLE", "true")
	t.Setenv("MAGUS_CONCURRENCY", "6")
	t.Setenv("MAGUS_DRY_RUN", "1")

	cfg := config.Defaults()
	ApplyEnv(&cfg, os.Getenv)

	assert.True(t, cfg.Cache.Immutable)
	assert.Equal(t, 6, cfg.Concurrency)
	assert.True(t, cfg.DryRun, "DryRun should be true")
}

func TestApplyEnv_SandboxEnabled(t *testing.T) {
	t.Setenv("MAGUS_SANDBOX_ENABLED", "true")
	cfg := config.Defaults()
	ApplyEnv(&cfg, os.Getenv)
	assert.True(t, cfg.Sandbox.Enabled, "MAGUS_SANDBOX_ENABLED=true: Sandbox.Enabled should be true")
}
