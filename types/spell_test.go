package types

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpellVersionProbe covers the toolchain version-probe accessor: a spell
// with no probe reports false and returns ("", nil); a spell with a probe
// surfaces its result (and error) verbatim.
func TestSpellVersionProbe(t *testing.T) {
	none := NewSpell("none")
	assert.False(t, none.HasVersionProbe(), "HasVersionProbe should be false for a spell with no probe")
	v, err := none.ProbeVersion(context.Background(), "/dir")
	assert.NoError(t, err)
	assert.Empty(t, v)

	probed := NewSpell("go", WithVersionProbe(func(_ context.Context, dir string) (string, error) {
		return "ver@" + dir, nil
	}))
	assert.True(t, probed.HasVersionProbe(), "HasVersionProbe should be true for a spell with a probe")
	v, err = probed.ProbeVersion(context.Background(), "/d")
	assert.NoError(t, err)
	assert.Equal(t, "ver@/d", v)

	boom := errors.New("boom")
	failing := NewSpell("rs", WithVersionProbe(func(_ context.Context, _ string) (string, error) {
		return "", boom
	}))
	_, err = failing.ProbeVersion(context.Background(), "/d")
	assert.ErrorIs(t, err, boom)
}

func TestNewSpellAccessors(t *testing.T) {
	s := NewSpell(
		"go",
		WithSources("**/*.go"),
		WithClaims("**/*.go"),
		WithSpellOutputs("bin/**"),
		WithTargets("build", "test"),
		WithOpaque(),
	)
	assert.Equal(t, "go", s.Name())
	assert.Equal(t, []string{"build", "test"}, s.Targets())
	assert.True(t, s.Opaque())
}

// WithTargetSources must defensively clone its input so a caller mutating the
// original map after construction cannot corrupt the spell's internal state.
func TestWithTargetSourcesClonesInput(t *testing.T) {
	src := map[string][]string{"test": {"testdata/**"}}
	s := NewSpell("go", WithTargetSources(src))

	// Mutate the caller's map after construction.
	src["test"] = []string{"hacked"}
	src["lint"] = []string{"added"}

	assert.Equal(t, []string{"testdata/**"}, s.TargetSources()["test"], "input map must be cloned")
	assert.Nil(t, s.TargetSources()["lint"], "keys added after construction must not leak in")
}

func TestSpellNilInvokerIsNoop(t *testing.T) {
	// A spell with no invoke function is a graceful no-op, not a panic.
	s := NewSpell("noop")
	resp, err := s.Invoke(t.Context(), InvokeRequest{Target: "build"})
	assert.NoError(t, err)
	assert.Equal(t, InvokeResponse{}, resp)
}

func TestSpellImplementsSpellDriver(t *testing.T) {
	require.Implements(t, (*SpellDriver)(nil), NewSpell("x"))
}

// TestSpellMetadataAccessors covers the plain metadata accessors that read back
// what the option setters stored.
func TestSpellMetadataAccessors(t *testing.T) {
	s := NewSpell("go",
		WithClaims("**/*.go"),
		WithLanguage("go"),
		WithTargetCharms(map[string][]string{"lint": {"rw", "gha"}}),
		WithTargetDocs(map[string]string{"build": "compile the binary"}),
		WithDocRequiredTargets("build", "test"),
		WithDeclarationFiles("go.mod"),
		WithDeclarationDirGlobs("cmd/**"),
	)
	assert.Equal(t, []string{"**/*.go"}, s.Claims())
	assert.Equal(t, "go", s.Language())
	assert.Equal(t, []string{"rw", "gha"}, s.Charms("lint"))
	assert.Nil(t, s.Charms("missing"))
	assert.Equal(t, "compile the binary", s.TargetDoc("build"))
	assert.Empty(t, s.TargetDoc("undocumented"))
	assert.Equal(t, []string{"build", "test"}, s.DocRequiredTargets())
	assert.Equal(t, []string{"go.mod"}, s.DeclarationFiles())
	assert.Equal(t, []string{"cmd/**"}, s.DeclarationDirGlobs())
}

// TestSpellDependsOn covers the in-workspace dependency probe: nil probe returns
// nil, a set probe is called with the project dir and its result surfaced.
func TestSpellDependsOn(t *testing.T) {
	assert.Nil(t, NewSpell("none").DependsOn("/dir"), "a spell with no probe returns nil")

	s := NewSpell("go", WithSpellDependsOn(func(dir string) []string {
		return []string{dir + "/dep"}
	}))
	assert.Equal(t, []string{"/proj/dep"}, s.DependsOn("/proj"))
}

// TestSpellCommandPreview covers the static preview accessors (render/explain/
// conflicts/service view): each returns ok=false when its func is unset, and
// forwards verbatim when set.
func TestSpellCommandPreview(t *testing.T) {
	// Unset: every preview accessor is a graceful (zero, false, nil).
	bare := NewSpell("bare")
	cmd, args, ok, err := bare.RenderCommand("build", nil)
	assert.Empty(t, cmd)
	assert.Nil(t, args)
	assert.False(t, ok)
	assert.NoError(t, err)

	steps, ok, err := bare.ExplainCommand("build", nil)
	assert.Nil(t, steps)
	assert.False(t, ok)
	assert.NoError(t, err)

	conflicts, ok, err := bare.ConflictingCharms("build", nil)
	assert.Nil(t, conflicts)
	assert.False(t, ok)
	assert.NoError(t, err)

	view, ok := bare.ServiceView("serve")
	assert.Nil(t, view)
	assert.False(t, ok)

	// Set: each accessor forwards target/charms and returns the func's result.
	wantSteps := []CharmTraceStep{{Command: []string{"go", "build"}}}
	wantConflicts := []CharmConflict{{Name: "a", OverriddenBy: "b"}}
	wantView := &ServiceView{Readiness: []string{"curl", "localhost"}, Idle: "30m"}
	s := NewSpell("go",
		WithCommandRenderer(func(target string, charms []string) (string, []string, bool, error) {
			return "go", append([]string{target}, charms...), true, nil
		}),
		WithCommandExplainer(func(string, []string) ([]CharmTraceStep, bool, error) {
			return wantSteps, true, nil
		}),
		WithCommandConflicts(func(string, []string) ([]CharmConflict, bool, error) {
			return wantConflicts, true, nil
		}),
		WithServiceView(func(string) (*ServiceView, bool) {
			return wantView, true
		}),
	)

	cmd, args, ok, err = s.RenderCommand("build", []string{"rw"})
	assert.Equal(t, "go", cmd)
	assert.Equal(t, []string{"build", "rw"}, args)
	assert.True(t, ok)
	assert.NoError(t, err)

	steps, ok, err = s.ExplainCommand("build", nil)
	assert.Equal(t, wantSteps, steps)
	assert.True(t, ok)
	assert.NoError(t, err)

	conflicts, ok, err = s.ConflictingCharms("build", nil)
	assert.Equal(t, wantConflicts, conflicts)
	assert.True(t, ok)
	assert.NoError(t, err)

	view, ok = s.ServiceView("serve")
	assert.Equal(t, wantView, view)
	assert.True(t, ok)
}

// TestSpellInvokerForwards covers WithInvoker: the invoker's Data and error are
// carried out through InvokeResponse verbatim.
func TestSpellInvokerForwards(t *testing.T) {
	s := NewSpell("go", WithInvoker(func(_ context.Context, req InvokeRequest) (any, error) {
		return "ran:" + req.Target, nil
	}))
	resp, err := s.Invoke(context.Background(), InvokeRequest{Target: "build"})
	assert.NoError(t, err)
	assert.Equal(t, InvokeResponse{Data: "ran:build"}, resp)
}

func TestSpellServiceTargets(t *testing.T) {
	s := NewSpell("node", WithTargets("build", "serve"), WithServiceTargets("serve"))
	assert.True(t, s.IsServiceTarget("serve"), "serve is a service op")
	assert.False(t, s.IsServiceTarget("build"), "build is a command op")
	assert.False(t, s.IsServiceTarget("missing"), "unknown target is not a service")

	// A spell with no service targets never reports one.
	assert.False(t, NewSpell("go", WithTargets("build")).IsServiceTarget("build"))

	// An empty WithServiceTargets is a no-op: it leaves the map nil.
	assert.False(t, NewSpell("go", WithServiceTargets()).IsServiceTarget("build"))
}
