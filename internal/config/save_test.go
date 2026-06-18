package config

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
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
		if err := Save(path, c.key, c.value); err != nil {
			t.Fatalf("Save(%s=%s): %v", c.key, c.value, err)
		}
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Hints.Enabled == nil || *cfg.Hints.Enabled {
		t.Errorf("hints.enabled = %v, want false", cfg.Hints.Enabled)
	}
	if cfg.Flake.Threshold != 0.25 {
		t.Errorf("flake.threshold = %v, want 0.25", cfg.Flake.Threshold)
	}
	if cfg.Daemon.IdleTTL != 30*time.Minute {
		t.Errorf("daemon.idle_ttl = %v, want 30m", cfg.Daemon.IdleTTL)
	}
	if cfg.Telemetry.Headers["Authorization"] != "Bearer xyz" {
		t.Errorf("telemetry.headers = %v, want Authorization=Bearer xyz", cfg.Telemetry.Headers)
	}
	var got *SandboxAllowPath
	for i := range cfg.Sandbox.Allow {
		if cfg.Sandbox.Allow[i].Name == "homebin" {
			got = &cfg.Sandbox.Allow[i]
		}
	}
	if got == nil || got.Path != "/home/user/.local/bin" || got.Mode != "ro" {
		t.Errorf("sandbox.allow homebin = %+v, want {path:/home/user/.local/bin mode:ro}", got)
	}
}

func TestKnownKeys(t *testing.T) {
	keys := KnownKeys()
	if len(keys) == 0 {
		t.Fatal("KnownKeys returned empty list")
	}
	if !slices.IsSorted(keys) {
		t.Errorf("KnownKeys not sorted: %v", keys)
	}
	want := map[string]bool{
		"cache.dir": true, "cache.size_mb": true, "cache.immutable": true, "cache.remote.trusted_keys": true, "cache.remote.insecure": true,
		"ci.max_shards": true, "ci.runner_pool_budget": true,
		"flake.enabled": true, "flake.bootstrap_samples": true, "flake.min_samples": true, "flake.annotate_gha": true, "flake.threshold": true,
		"daemon.idle_ttl": true,
		"hints.enabled":   true, "mcp.enabled": true, "vcs.enabled": true,
		"telemetry.headers": true, "telemetry.sample_ratio": true,
		"sandbox.allow.<name>.name": true, "sandbox.allow.<name>.path": true, "sandbox.allow.<name>.mode": true,
		"graph.direction": true, "graph.spell": true, "graph.depth": true, "graph.roots": true,
		"health.exempt": true,
		"log.format":    true, "log.level": true, "concurrency": true, "history_path": true, "dry_run": true, "strict": true,
		"mcp.address":       true,
		"telemetry.enabled": true, "telemetry.endpoint": true,
		"telemetry.protocol": true, "telemetry.insecure": true,
		"telemetry.service_name": true,
		"daemon.address":         true, "daemon.socket": true, "daemon.workspaces": true,
		"vcs.base_ref": true, "vcs.name": true,
		"assume_interactive":      true,
		"report.filter":           true,
		"sandbox.enabled":         true,
		"sandbox.env.passthrough": true,
	}
	for _, k := range keys {
		if !want[k] {
			t.Errorf("unexpected key %q", k)
		}
		delete(want, k)
	}
	for k := range want {
		t.Errorf("missing expected key %q", k)
	}
}

func TestSave_CreatesMissingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	if err := Save(path, "cache.dir", "/tmp/mydir"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Cache.Dir != "/tmp/mydir" {
		t.Errorf("got Cache.Dir=%q, want %q", cfg.Cache.Dir, "/tmp/mydir")
	}
}

func TestSave_MutatesOnlyTouchedKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	if err := os.WriteFile(path, []byte("concurrency: 4\nlog:\n  format: plain\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Save(path, "cache.dir", "/data/cache"); err != nil {
		t.Fatalf("Save: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Cache.Dir != "/data/cache" {
		t.Errorf("Cache.Dir = %q, want %q", cfg.Cache.Dir, "/data/cache")
	}
	if cfg.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4", cfg.Concurrency)
	}
	if cfg.Log.Format != "plain" {
		t.Errorf("Log.Format = %q, want %q", cfg.Log.Format, "plain")
	}
}

func TestSave_IntValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	if err := Save(path, "concurrency", "banana"); err == nil {
		t.Error("expected error for non-int value, got nil")
	}
	if err := Save(path, "concurrency", "8"); err != nil {
		t.Fatalf("Save int: %v", err)
	}
	cfg, _ := Load(path)
	if cfg.Concurrency != 8 {
		t.Errorf("Concurrency = %d, want 8", cfg.Concurrency)
	}
}

func TestSave_BoolValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	for _, bad := range []string{"maybe", "y", "nope"} {
		if err := Save(path, "dry_run", bad); err == nil {
			t.Errorf("expected error for %q, got nil", bad)
		}
	}

	for _, good := range []string{"true", "1", "false", "0"} {
		if err := Save(path, "dry_run", good); err != nil {
			t.Errorf("Save bool %q: %v", good, err)
		}
	}

	if err := Save(path, "dry_run", "true"); err != nil {
		t.Fatal(err)
	}
	cfg, _ := Load(path)
	if !cfg.DryRun {
		t.Error("DryRun = false, want true")
	}
}

func TestSave_UnknownKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")
	err := Save(path, "nonexistent.key", "value")
	if err == nil {
		t.Error("expected error for unknown key, got nil")
	}
}

// TestInit_WritesBuiltinDefaults verifies that Init writes a valid config
// file, refuses overwrite without --force, and obeys --force when set.
func TestInit_WritesBuiltinDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	if err := Init(path, false); err != nil {
		t.Fatalf("Init: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// Cache defaults to mutable (immutable = false).
	if cfg.Cache.Immutable {
		t.Error("Init: Cache.Immutable = true, want false (default)")
	}

	if err := Init(path, false); err == nil {
		t.Error("expected refusal to overwrite, got nil")
	}
	if err := Init(path, true); err != nil {
		t.Errorf("Init --force: %v", err)
	}
}

// TestSave_RejectsInvalidScalar verifies that validation fires
// before the file is written for scalar fields with constrained values.
func TestSave_RejectsInvalidScalar(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	if err := os.WriteFile(path, []byte("log:\n  format: json\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)

	err := Save(path, "log.format", "bogus")
	if err == nil {
		t.Fatal("Save accepted invalid log value, want validation error")
	}
	var ve *ValidationError
	if !errors.As(err, &ve) {
		t.Errorf("err = %T, want *ValidationError", err)
	}

	now, _ := os.ReadFile(path)
	if string(original) != string(now) {
		t.Errorf("file mutated despite validation failure")
	}
}

// TestLoad_RejectsInvalidShardCount verifies the shard_count custom
// validator catches out-of-range CI shard counts.
func TestLoad_RejectsInvalidShardCount(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "magus.yaml")

	if err := os.WriteFile(path, []byte("ci:\n  max_shards: 999\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load accepted max_shards=999, want validation error")
	}
	if !strings.Contains(err.Error(), "ci.max_shards") {
		t.Errorf("err %q does not mention ci.max_shards", err)
	}
}

func TestLoad_DaemonAddressValidation(t *testing.T) {
	cases := []struct {
		yaml    string
		wantErr bool
	}{
		{"daemon:\n  address: unix:///tmp/magus.sock\n", false},
		{"daemon:\n  address: \"\"\n", false},
		{"daemon:\n  address: /tmp/magus.sock\n", true},
		{"daemon:\n  address: tcp://localhost:9000\n", true},
		{"daemon:\n  address: unix://\n", true},
	}
	for _, tc := range cases {
		dir := t.TempDir()
		path := filepath.Join(dir, "magus.yaml")
		if err := os.WriteFile(path, []byte(tc.yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if tc.wantErr && err == nil {
			t.Errorf("yaml %q: expected error, got nil", tc.yaml)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("yaml %q: unexpected error: %v", tc.yaml, err)
		}
	}
}

func TestSave_CreatesParentDirectory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "magus.yaml")

	if err := Save(path, "log.format", "json"); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("file not created: %v", err)
	}
}
