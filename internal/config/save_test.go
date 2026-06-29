package config

import (
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSave_AllValueTypes exercises config set across every value kind: a *bool,
// a float, a duration, a map, and a name-addressed slice-of-struct entry
// (sandbox.allow). Each must Save and Load back to the expected typed value.
func TestSave_AllValueTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "magus.yaml")
	for _, c := range []struct{ key, value string }{
		{"hints.enabled", "false"},                              // *bool
		{"flake.threshold", "0.25"},                             // float64
		{"daemon.idle_ttl", "30m"},                              // time.Duration
		{"telemetry.headers", "{Authorization: Bearer xyz}"},    // map[string]string
		{"sandbox.allow.homebin.path", "/home/user/.local/bin"}, // slice-of-struct (by name)
		{"sandbox.allow.homebin.mode", "ro"},
	} {
		require.NoError(t, Save(path, c.key, c.value), "Save(%s=%s)", c.key, c.value)
	}

	cfg, err := Load(path)
	require.NoError(t, err)
	require.NotNil(t, cfg.Hints.Enabled)
	assert.False(t, *cfg.Hints.Enabled, "hints.enabled should be false")
	assert.Equal(t, 0.25, cfg.Flake.Threshold)
	assert.Equal(t, 30*time.Minute, cfg.Daemon.IdleTTL)
	assert.Equal(t, "Bearer xyz", cfg.Telemetry.Headers["Authorization"])

	var got *SandboxAllowPath
	for i := range cfg.Sandbox.Allow {
		if cfg.Sandbox.Allow[i].Name == "homebin" {
			got = &cfg.Sandbox.Allow[i]
		}
	}
	require.NotNil(t, got, "sandbox.allow homebin entry not found")
	assert.Equal(t, "/home/user/.local/bin", got.Path)
	assert.Equal(t, "ro", got.Mode)
}

func TestKnownKeys(t *testing.T) {
	keys := KnownKeys()
	require.NotEmpty(t, keys, "KnownKeys returned empty list")
	assert.True(t, slices.IsSorted(keys), "KnownKeys not sorted: %v", keys)
	want := map[string]bool{
		"cache.dir": true, "cache.size_mb": true, "cache.immutable": true, "cache.remote.trusted_keys": true, "cache.remote.insecure": true,
		"ci.max_shards": true, "ci.runner_pool_budget": true,
		"flake.enabled": true, "flake.bootstrap_samples": true, "flake.min_samples": true, "flake.annotate_gha": true, "flake.threshold": true,
		"daemon.idle_ttl": true,
		"hints.enabled":   true, "mcp.enabled": true, "vcs.enabled": true,
		"telemetry.headers": true, "telemetry.sample_ratio": true,
		"sandbox.allow.<name>.name": true, "sandbox.allow.<name>.path": true, "sandbox.allow.<name>.mode": true,
		"graph.direction": true, "graph.spell": true, "graph.depth": true, "graph.roots": true,
		"log.format": true, "log.level": true, "log.silent": true, "concurrency": true, "history_path": true, "dry_run": true,
		"mcp.address":       true,
		"telemetry.enabled": true, "telemetry.endpoint": true,
		"telemetry.protocol": true, "telemetry.insecure": true,
		"telemetry.service_name": true,
		"daemon.address":         true, "daemon.socket": true, "daemon.workspaces": true,
		"vcs.base_ref": true, "vcs.name": true,
		"assume_interactive":      true,
		"default_charms":          true,
		"report.filter":           true,
		"sandbox.enabled":         true,
		"sandbox.env.passthrough": true,
	}
	for _, k := range keys {
		assert.True(t, want[k], "unexpected key %q", k)
		delete(want, k)
	}
	assert.Empty(t, want, "missing expected keys: %v", want)
}

func TestSave_CreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	require.NoError(t, Save(path, "cache.dir", "/tmp/mydir"))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "/tmp/mydir", cfg.Cache.Dir)
}

func TestSave_MutatesOnlyTouchedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	require.NoError(t, os.WriteFile(path, []byte("concurrency: 4\nlog:\n  format: plain\n"), 0o644))
	require.NoError(t, Save(path, "cache.dir", "/data/cache"))

	cfg, err := Load(path)
	require.NoError(t, err)
	assert.Equal(t, "/data/cache", cfg.Cache.Dir)
	assert.Equal(t, 4, cfg.Concurrency)
	assert.Equal(t, "plain", cfg.Log.Format)
}

func TestSave_IntValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	assert.Error(t, Save(path, "concurrency", "banana"), "expected error for non-int value")
	require.NoError(t, Save(path, "concurrency", "8"))
	cfg, _ := Load(path)
	assert.Equal(t, 8, cfg.Concurrency)
}

func TestSave_BoolValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	for _, bad := range []string{"maybe", "y", "nope"} {
		assert.Error(t, Save(path, "dry_run", bad), "expected error for %q", bad)
	}

	for _, good := range []string{"true", "1", "false", "0"} {
		assert.NoError(t, Save(path, "dry_run", good), "Save bool %q", good)
	}

	require.NoError(t, Save(path, "dry_run", "true"))
	cfg, _ := Load(path)
	assert.True(t, cfg.DryRun, "DryRun should be true")
}

func TestSave_UnknownKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")
	assert.Error(t, Save(path, "nonexistent.key", "value"), "expected error for unknown key")
}

// TestInit_WritesBuiltinDefaults verifies that Init writes a valid config
// file, refuses overwrite without --force, and obeys --force when set.
func TestInit_WritesBuiltinDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	require.NoError(t, Init(path, false))

	cfg, err := Load(path)
	require.NoError(t, err)
	// Cache defaults to mutable (immutable = false).
	assert.False(t, cfg.Cache.Immutable, "Init: Cache.Immutable should be false (default)")

	assert.Error(t, Init(path, false), "expected refusal to overwrite")
	assert.NoError(t, Init(path, true), "Init --force")
}

// TestSave_RejectsInvalidScalar verifies that validation fires
// before the file is written for scalar fields with constrained values.
func TestSave_RejectsInvalidScalar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	require.NoError(t, os.WriteFile(path, []byte("log:\n  format: json\n"), 0o644))
	original, _ := os.ReadFile(path)

	err := Save(path, "log.format", "bogus")
	require.Error(t, err, "Save accepted invalid log value, want validation error")
	var ve *ValidationError
	assert.ErrorAs(t, err, &ve)

	now, _ := os.ReadFile(path)
	assert.Equal(t, string(original), string(now), "file mutated despite validation failure")
}

// TestLoad_RejectsInvalidShardCount verifies the shard_count custom
// validator catches out-of-range CI shard counts.
func TestLoad_RejectsInvalidShardCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	require.NoError(t, os.WriteFile(path, []byte("ci:\n  max_shards: 999\n"), 0o644))
	_, err := Load(path)
	require.Error(t, err, "Load accepted max_shards=999, want validation error")
	assert.Contains(t, err.Error(), "ci.max_shards")
}

func TestLoad_DaemonAddressValidation(t *testing.T) {
	check := func(t *testing.T, yamlContent string, wantErr bool) {
		dir := t.TempDir()
		path := filepath.Join(dir, "magus.yaml")
		require.NoError(t, os.WriteFile(path, []byte(yamlContent), 0o644))
		_, err := Load(path)
		if wantErr {
			assert.Error(t, err)
		} else {
			assert.NoError(t, err)
		}
	}

	t.Run("unix valid", func(t *testing.T) {
		check(t, "daemon:\n  address: unix:///tmp/magus.sock\n", false)
	})
	t.Run("empty address", func(t *testing.T) {
		check(t, "daemon:\n  address: \"\"\n", false)
	})
	t.Run("bare path rejected", func(t *testing.T) {
		check(t, "daemon:\n  address: /tmp/magus.sock\n", true)
	})
	t.Run("tcp scheme rejected", func(t *testing.T) {
		check(t, "daemon:\n  address: tcp://localhost:9000\n", true)
	})
	t.Run("unix empty path rejected", func(t *testing.T) {
		check(t, "daemon:\n  address: unix://\n", true)
	})
}

func TestSave_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "magus.yaml")

	require.NoError(t, Save(path, "log.format", "json"))
	_, err := os.Stat(path)
	assert.NoError(t, err, "file not created")
}
