package config

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMergeConfig(t *testing.T) {
	t.Parallel()
	base := Defaults()
	base.Concurrency = 4

	overlay := Config{}
	overlay.Cache.Immutable = true
	overlay.Cache.Dir = "/tmp/cache"

	got := mergeConfig(base, overlay)
	assert.True(t, got.Cache.Immutable)
	assert.Equal(t, "/tmp/cache", got.Cache.Dir)
	// base value preserved when overlay is zero
	assert.Equal(t, 4, got.Concurrency)
}

func TestLoadDirInto(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "cache:\n  immutable: true\nconcurrency: 12\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte(content), 0o644))

	cfg, err := loadDirInto(Defaults(), dir)
	require.NoError(t, err)
	assert.True(t, cfg.Cache.Immutable)
	assert.Equal(t, 12, cfg.Concurrency)
}

func TestLoadDirIntoDotted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "cache:\n  immutable: true\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".magus.yaml"), []byte(content), 0o644))

	cfg, err := loadDirInto(Defaults(), dir)
	require.NoError(t, err)
	assert.True(t, cfg.Cache.Immutable)
}

func TestLoadDirIntoCoexistenceError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte(""), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".magus.yaml"), []byte(""), 0o644))

	_, err := loadDirInto(Defaults(), dir)
	assert.Error(t, err, "expected error for coexisting magus.yaml and .magus.yaml")
}

func TestLoadDirIntoMissing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	base := Defaults()
	cfg, err := loadDirInto(base, dir)
	require.NoError(t, err)
	// No file → cfg is unchanged from base
	assert.Equal(t, base.Cache.Immutable, cfg.Cache.Immutable, "Cache.Immutable changed unexpectedly")
}

func TestWarnIfConcurrencyHigh(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, concurrency, numCPU int, wantWarn bool) {
		// slog.SetDefault mutates global state — subtests cannot run in parallel.
		var buf bytes.Buffer
		prev := slog.Default()
		slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
		t.Cleanup(func() { slog.SetDefault(prev) })

		warnIfConcurrencyHigh(concurrency, numCPU)

		got := strings.Contains(buf.String(), "config.concurrency_high")
		assert.Equal(t, wantWarn, got, "warn emitted mismatch (log=%q)", buf.String())
	}

	t.Run("default unset", func(t *testing.T) { run(t, 0, 8, false) })
	t.Run("at limit", func(t *testing.T) { run(t, 16, 8, false) })
	t.Run("just over", func(t *testing.T) { run(t, 17, 8, true) })
	t.Run("way over", func(t *testing.T) { run(t, 200, 8, true) })
	t.Run("unknown cpu", func(t *testing.T) { run(t, 16, 0, false) })
}
