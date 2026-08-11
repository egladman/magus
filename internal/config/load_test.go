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
	writeOff := false
	overlay.Cache.Write.Enabled = &writeOff
	overlay.Cache.Dir = "/tmp/cache"

	got := mergeConfig(base, overlay)
	assert.False(t, got.Cache.WriteEnabled())
	assert.Equal(t, "/tmp/cache", got.Cache.Dir)
	// base value preserved when overlay is zero
	assert.Equal(t, 4, got.Concurrency)
}

func TestLoadDirInto(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "cache:\n  write:\n    enabled: false\nconcurrency: 12\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "magus.yaml"), []byte(content), 0o644))

	cfg, err := loadDirInto(Defaults(), dir)
	require.NoError(t, err)
	assert.False(t, cfg.Cache.WriteEnabled())
	assert.Equal(t, 12, cfg.Concurrency)
}

func TestLoadDirIntoDotted(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	content := "cache:\n  write:\n    enabled: false\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".magus.yaml"), []byte(content), 0o644))

	cfg, err := loadDirInto(Defaults(), dir)
	require.NoError(t, err)
	assert.False(t, cfg.Cache.WriteEnabled())
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
	assert.Equal(t, base.Cache.WriteEnabled(), cfg.Cache.WriteEnabled(), "Cache.Write.Enabled changed unexpectedly")
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

// TestExtractFlag pins every spelling ExtractFlag must recognize for -config/--config,
// including the short -c form main.go advertises in its help text but never actually wired
// through (cfgPath had zero readers after fs.Parse). -C is a DIFFERENT flag (short for
// --root, bound only in cmd/magus/main.go) and flag matching is case-sensitive, so -C must
// never be read as config.
func TestExtractFlag(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		args []string
		want string
	}{
		{"long form space", []string{"-config", "a.yaml"}, "a.yaml"},
		{"long form dashdash space", []string{"--config", "a.yaml"}, "a.yaml"},
		{"long form equals", []string{"-config=a.yaml"}, "a.yaml"},
		{"long form dashdash equals", []string{"--config=a.yaml"}, "a.yaml"},
		{"short form space", []string{"-c", "a.yaml"}, "a.yaml"},
		{"short form dashdash space", []string{"--c", "a.yaml"}, "a.yaml"},
		{"short form equals", []string{"-c=a.yaml"}, "a.yaml"},
		{"short form dashdash equals", []string{"--c=a.yaml"}, "a.yaml"},
		{"short form among other args", []string{"run", "-c", "a.yaml", "build"}, "a.yaml"},
		{"missing value", []string{"-c"}, ""},
		{"no flag at all", []string{"run", "build"}, ""},
		{"-C is root, not config", []string{"-C", "a.yaml"}, ""},
		{"--root is not config either", []string{"--root", "a.yaml"}, ""},
		{"stops at -- separator", []string{"run", "build", "--", "-c", "a.yaml"}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, ExtractFlag(tc.args))
		})
	}
}
