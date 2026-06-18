package gen

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConfigFlagsNotDrifted re-runs the flag generator into a temp file and
// diffs it against the committed config_flags.go. The test fails if the
// committed file is out of date, which means a Config change requires
// re-running:
//
//	go generate ./cmd/magus/...
func TestConfigFlagsNotDrifted(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping drift check in short mode")
	}

	_, thisFile, _, ok := runtime.Caller(0)
	require.True(t, ok, "runtime.Caller failed")
	genDir := filepath.Dir(thisFile)

	tmp := t.TempDir()
	flagsGen := filepath.Join(tmp, "config_flags.go")

	cmd := exec.Command("go", "run",
		"../../magus-config-gen",
		"-config", "../../../internal/config/config.go",
		"-out", flagsGen,
	)
	cmd.Dir = genDir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "config/flag generator failed:\n%s", out)

	want, err := os.ReadFile(filepath.Join(genDir, "config_flags.go"))
	require.NoError(t, err, "read committed config_flags.go")
	got, err := os.ReadFile(flagsGen)
	require.NoError(t, err, "read generated config_flags.go")
	assert.Equal(t, string(want), string(got), "config_flags.go is out of date — run: go generate ./cmd/magus/...")
}
