package sandbox

import (
	"io"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/interactive"
	"github.com/egladman/magus/internal/sandbox/filesystem"
)

func TestDenyHint(t *testing.T) {
	t.Parallel()

	got := denyHint("", filesystem.Read, "/data/in")
	assert.Equal(t, "sandbox blocked access to /data/in; allow it with:\n"+
		"        magus config set key=sandbox.allow.in.path,value=/data/in", got,
		"read is the default mode, so it needs no mode command")

	w := denyHint("", filesystem.Write, "/data/out")
	assert.Contains(t, w, "sandbox.allow.out.path,value=/data/out")
	assert.Contains(t, w, "sandbox.allow.out.mode,value=rw")

	// Exec is granted explicitly, so its remedy names the mode that grants it.
	x := denyHint("", filesystem.Exec, "/usr/local/bin/curl")
	assert.Contains(t, x, "sandbox.allow.curl.mode,value=rx")

	// Under a lease the boundary is the row, not the config: the hint names the
	// lease and never tells the worker to widen sandbox.allow itself.
	leased := denyHint("fleet/worker-1", filesystem.Write, "/data/out")
	assert.Contains(t, leased, "fleet/worker-1")
	assert.Contains(t, leased, "orchestrator")
	assert.NotContains(t, leased, "magus config set")
}

// captureStderr runs fn with os.Stderr redirected. Tests using it are not parallel:
// they swap a process-wide file and flip the process-wide hints switch.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	require.NoError(t, err)
	orig := os.Stderr
	os.Stderr = w
	fn()
	os.Stderr = orig
	require.NoError(t, w.Close())
	out, err := io.ReadAll(r)
	require.NoError(t, err)
	return string(out)
}

func TestEmitDenyHint(t *testing.T) {
	interactive.SetHintsEnabled(true)
	defer interactive.SetHintsEnabled(true)
	emit := func() { EmitDenyHint(nil, filesystem.Exec, "/usr/bin/curl") }

	got := captureStderr(t, emit)
	assert.Contains(t, got, "hint:")
	assert.Contains(t, got, "magus config set key=sandbox.allow.curl.path,value=/usr/bin/curl")

	interactive.SetHintsEnabled(false)
	assert.Empty(t, captureStderr(t, emit), "EmitDenyHint should be silent when hints are disabled")
}

// TestDetectShimSuspect covers the case MGS2006 names: a manager's shim directory is
// still on PATH (so the shim binary still runs) but the var it reads to pick a tool
// version was scrubbed.
func TestDetectShimSuspect(t *testing.T) {
	for _, tc := range []struct {
		name            string
		policy          *Policy
		manager, envVar string
	}{
		{"mise shim on PATH, var dropped", &Policy{
			BaseEnv:    []string{"PATH=/home/u/.local/share/mise/shims:/usr/bin"},
			EnvDropped: []string{"MISE_DATA_DIR", "GITHUB_TOKEN"},
		}, "mise", "MISE_DATA_DIR"},
		{"asdf shim on PATH, var dropped", &Policy{
			BaseEnv:    []string{"PATH=/home/u/.asdf/shims:/usr/bin"},
			EnvDropped: []string{"ASDF_DIR"},
		}, "asdf", "ASDF_DIR"},
		{"no shim on PATH", &Policy{
			BaseEnv:    []string{"PATH=/usr/bin:/usr/local/bin"},
			EnvDropped: []string{"MISE_DATA_DIR"},
		}, "", ""},
		{"shim on PATH, var kept", &Policy{
			BaseEnv:    []string{"PATH=/home/u/.local/share/mise/shims:/usr/bin"},
			EnvDropped: []string{"GITHUB_TOKEN"},
		}, "", ""},
		{"sandbox off", nil, "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manager, envVar, ok := detectShimSuspect(tc.policy)
			assert.Equal(t, tc.manager != "", ok)
			assert.Equal(t, tc.manager, manager)
			assert.Equal(t, tc.envVar, envVar)
		})
	}
}

// TestShimHint pins the MGS2006 message shape against
// docs/reference/codes/sandbox/MGS2006.md's printed example.
func TestShimHint(t *testing.T) {
	got := shimHint("go", "mise", "MISE_DATA_DIR")
	assert.Contains(t, got, "MGS2006")
	assert.Contains(t, got, "mise shims appear stripped from PATH; the build is using system tools instead")
	assert.Contains(t, got, "cmd=go missing_var=MISE_DATA_DIR")
}

func TestEmitShimHint(t *testing.T) {
	interactive.SetHintsEnabled(true)
	defer interactive.SetHintsEnabled(true)

	suspect := &Policy{
		BaseEnv:    []string{"PATH=/home/u/.local/share/mise/shims:/usr/bin"},
		EnvDropped: []string{"MISE_DATA_DIR"},
	}
	got := captureStderr(t, func() { EmitShimHint(suspect, "go") })
	assert.Contains(t, got, "hint:")
	assert.Contains(t, got, "MGS2006")
	assert.Contains(t, got, "cmd=go missing_var=MISE_DATA_DIR")

	assert.Empty(t, captureStderr(t, func() { EmitShimHint(nil, "go") }), "silent with sandbox off")
	assert.Empty(t, captureStderr(t, func() { EmitShimHint(&Policy{}, "go") }), "silent when nothing is suspect")

	interactive.SetHintsEnabled(false)
	assert.Empty(t, captureStderr(t, func() { EmitShimHint(suspect, "go") }), "silent when hints are disabled")
}
