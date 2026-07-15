package magus

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiagEventFromError(t *testing.T) {
	// A coded DiagnosticError yields an event tagged with the target identity.
	de := types.DiagnosticErrorf(types.ExecDenied, "exec denied: /bin/x")
	ev, ok := diagEventFromError("pkg/foo", "build", de)
	assert.True(t, ok)
	assert.Equal(t, types.ExecDenied, ev.Code)
	assert.Equal(t, "pkg/foo:build", ev.Unit)

	// A wrapped diagnostic error is still recognized (errors.As unwraps).
	wrapped := fmt.Errorf("run failed: %w", de)
	ev, ok = diagEventFromError("pkg/foo", "", wrapped)
	assert.True(t, ok)
	assert.Equal(t, "pkg/foo", ev.Unit, "no target -> project-scoped unit")

	// A nil or plain error is not a diagnostic event.
	_, ok = diagEventFromError("pkg/foo", "build", nil)
	assert.False(t, ok)
	_, ok = diagEventFromError("pkg/foo", "build", errors.New("boom"))
	assert.False(t, ok)
}

func TestMakeHandler_PreflightGenerateFireOnVariantSpellings(t *testing.T) {
	// makeHandler special-cases the exact strings "preflight"/"generate"
	// (run.go). types.ParseTarget normalizes the CLI's raw spelling before it
	// ever reaches makeHandler, so a variant invocation still resolves to the
	// canonical name the special-casing checks against.
	for _, in := range []string{"preflight", "Preflight", "PREFLIGHT"} {
		parsed, err := types.ParseTarget(in)
		assert.NoErrorf(t, err, "ParseTarget(%q)", in)
		assert.Equalf(t, "preflight", parsed.Name, "ParseTarget(%q).Name", in)
	}
	for _, in := range []string{"generate", "Generate", "GENERATE"} {
		parsed, err := types.ParseTarget(in)
		assert.NoErrorf(t, err, "ParseTarget(%q)", in)
		assert.Equalf(t, "generate", parsed.Name, "ParseTarget(%q).Name", in)
	}

	var m *Magus
	h := m.makeHandler("generate")
	assert.NotNil(t, h)
}

func TestRaceForcesNoCache(t *testing.T) {
	assert.False(t, raceForcesNoCache(run{}), "neither Race nor RaceReplay set")
	assert.True(t, raceForcesNoCache(run{Race: true}), "Race alone")
	assert.True(t, raceForcesNoCache(run{RaceReplay: true}), "RaceReplay alone")
	assert.True(t, raceForcesNoCache(run{Race: true, RaceReplay: true}), "both set")
}

// TestRun_RaceReexecutesCachedTarget guards the A2 fix end to end: a target
// that's already a cache hit must still genuinely re-execute under --race
// (magus.WithRace), not replay - otherwise the race detector observes nothing,
// per the plan this fixes.
func TestRun_RaceReexecutesCachedTarget(t *testing.T) {
	const spellName = "zzz-race-test-spell"
	var calls atomic.Int32
	spell := types.NewSpell(spellName,
		types.WithTargets("build"),
		types.WithInvoker(func(context.Context, types.InvokeRequest) (any, error) {
			calls.Add(1)
			return nil, nil
		}),
	)
	project.DefaultSpellRegistry().RegisterSpell(spell)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := Open(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })

	ctx := context.Background()
	targets := []types.Target{{Path: ".", Name: "build"}}

	require.NoError(t, m.Run(ctx, targets), "first run")
	assert.Equal(t, int32(1), calls.Load(), "first run: expected one real execution")

	require.NoError(t, m.Run(ctx, targets), "second run (should hit cache)")
	assert.Equal(t, int32(1), calls.Load(), "second run: cache hit must not re-execute")

	require.NoError(t, m.Run(ctx, targets, WithRace()), "third run (--race)")
	assert.Equal(t, int32(2), calls.Load(), "--race run: a cached target must still genuinely re-execute")
}

func TestDiagCollectorCollects(t *testing.T) {
	d := &diagCollector{} // nil report writer: RecordDiagnostic must still collect
	d.RecordDiagnostic(types.DiagnosticEvent{Unit: "a:build", Code: types.ExecDenied})
	d.RecordDiagnostic(types.DiagnosticEvent{Unit: "b:test", Code: types.RaceDetected})

	snap := d.snapshot()
	assert.Len(t, snap, 2)
	// snapshot is a copy: mutating it must not affect the collector.
	snap[0].Unit = "mutated"
	assert.Equal(t, "a:build", d.snapshot()[0].Unit)
}
