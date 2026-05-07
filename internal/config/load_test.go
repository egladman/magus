package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMergeConfig(t *testing.T) {
	t.Parallel()
	base := Defaults()
	base.Concurrency = 4

	overlay := Config{}
	overlay.Cache.Immutable = true
	overlay.Cache.Dir = "/tmp/cache"

	got := mergeConfig(base, overlay)
	if !got.Cache.Immutable {
		t.Errorf("Cache.Immutable = %v, want true", got.Cache.Immutable)
	}
	if got.Cache.Dir != "/tmp/cache" {
		t.Errorf("Cache.Dir = %q, want %q", got.Cache.Dir, "/tmp/cache")
	}
	// base value preserved when overlay is zero
	if got.Concurrency != 4 {
		t.Errorf("Concurrency = %d, want 4", got.Concurrency)
	}
}

func TestLoadDirInto(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "cache:\n  immutable: true\nconcurrency: 12\n"
	if err := os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadDirInto(Defaults(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Cache.Immutable {
		t.Errorf("Cache.Immutable = %v, want true", cfg.Cache.Immutable)
	}
	if cfg.Concurrency != 12 {
		t.Errorf("Concurrency = %d, want 12", cfg.Concurrency)
	}
}

func TestLoadDirIntoDotted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "cache:\n  immutable: true\n"
	if err := os.WriteFile(filepath.Join(dir, ".magus.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := loadDirInto(Defaults(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Cache.Immutable {
		t.Errorf("Cache.Immutable = %v, want true", cfg.Cache.Immutable)
	}
}

func TestLoadDirIntoCoexistenceError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".magus.yaml"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := loadDirInto(Defaults(), dir)
	if err == nil {
		t.Fatal("expected error for coexisting magus.yaml and .magus.yaml, got nil")
	}
}

func TestLoadDirIntoMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := Defaults()
	cfg, err := loadDirInto(base, dir)
	if err != nil {
		t.Fatal(err)
	}
	// No file → cfg is unchanged from base
	if cfg.Cache.Immutable != base.Cache.Immutable {
		t.Errorf("Cache.Immutable changed unexpectedly: %v vs %v", cfg.Cache.Immutable, base.Cache.Immutable)
	}
}

func TestWarnIfConcurrencyHigh(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		concurrency int
		numCPU      int
		wantWarn    bool
	}{
		{"default unset", 0, 8, false},
		{"at limit", 16, 8, false},
		{"just over", 17, 8, true},
		{"way over", 200, 8, true},
		{"unknown cpu", 16, 0, false},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// slog.SetDefault mutates global state — subtests cannot run in parallel.
			var buf bytes.Buffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
			t.Cleanup(func() { slog.SetDefault(prev) })

			warnIfConcurrencyHigh(tc.concurrency, tc.numCPU)

			got := strings.Contains(buf.String(), "config.concurrency_high")
			if got != tc.wantWarn {
				t.Errorf("warn emitted = %v, want %v (log=%q)", got, tc.wantWarn, buf.String())
			}
		})
	}
}
