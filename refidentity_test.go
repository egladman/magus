package magus

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// newIdentityWorkspace opens a real (cache-backed) single-project workspace bound to a
// spell providing target "build" - IdentifyRef needs a live cache (ComputeTargetKey
// returns types.ErrNoCache on an Inspect workspace), unlike the Inspect-only fixtures
// most describe_test.go tests use. defaultCharms, when given, becomes the workspace's
// configured default_charms (m.cfg.DefaultCharms) - what IdentifyRef now reads directly
// instead of taking as a parameter.
func newIdentityWorkspace(t *testing.T, defaultCharms ...string) *Magus {
	t.Helper()
	const spellName = "zzz-identify-spell"
	s := spells.NewSpell(spellName, spells.WithTargets("build"))
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	opts := []Option{WithWorkspaceRegistry(reg)}
	if len(defaultCharms) > 0 {
		opts = append(opts, WithLoadedConfig(config.Config{DefaultCharms: defaultCharms}))
	}
	m, err := Open(context.Background(), root, opts...)
	require.NoError(t, err, "Open")
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// TestIdentifyRef_RoundTrip is the whole feature: a ref minted from a target's live key
// must resolve straight back to that target.
func TestIdentifyRef_RoundTrip(t *testing.T) {
	m := newIdentityWorkspace(t)
	ctx := context.Background()

	key, _, err := m.ComputeTargetKey(ctx, ".", "build", nil)
	require.NoError(t, err, "ComputeTargetKey")
	ref := cache.PortableRef(key)

	matches, err := m.IdentifyRef(ctx, ref)
	require.NoError(t, err, "IdentifyRef")
	require.Len(t, matches, 1, "IdentifyRef: expected exactly one match")
	assert.Equal(t, types.RefMatch{Project: ".", Target: "build", Charms: nil}, matches[0])
}

// TestIdentifyRef_NoMatch guards the "nothing matched" path: a ref no live target keys
// to must come back as an empty slice, not an error.
func TestIdentifyRef_NoMatch(t *testing.T) {
	m := newIdentityWorkspace(t)
	ctx := context.Background()

	matches, err := m.IdentifyRef(ctx, "outdeadbeef0000")
	require.NoError(t, err, "IdentifyRef")
	assert.Empty(t, matches, "IdentifyRef: expected no matches")
}

// TestIdentifyRef_ShortenedPrefixMatches guards the git-style prefix rule: a truncated
// ref (as a human might paste a shortened one back) must still resolve, mirroring
// resolveRef's prefix semantics in internal/cache/output.go.
func TestIdentifyRef_ShortenedPrefixMatches(t *testing.T) {
	m := newIdentityWorkspace(t)
	ctx := context.Background()

	key, _, err := m.ComputeTargetKey(ctx, ".", "build", nil)
	require.NoError(t, err, "ComputeTargetKey")
	full := cache.PortableRef(key)
	short := full[:len(full)-4]
	require.NotEqual(t, full, short, "test setup: shortened ref should differ from full ref")

	matches, err := m.IdentifyRef(ctx, short)
	require.NoError(t, err, "IdentifyRef")
	require.Len(t, matches, 1, "IdentifyRef: expected exactly one match on shortened ref")
	assert.Equal(t, types.RefMatch{Project: ".", Target: "build", Charms: nil}, matches[0])
}

// TestIdentifyRef_CharmVariants guards the two-charm-set sweep: a ref minted under the
// workspace's configured default charms and a ref minted bare (as CI's
// --no-default-charms run would mint it) must BOTH resolve once the workspace has
// default_charms configured, since a pasted CI ref is exactly the case this method
// exists for.
func TestIdentifyRef_CharmVariants(t *testing.T) {
	defaultCharms := []string{"rw"}
	m := newIdentityWorkspace(t, defaultCharms...)
	ctx := context.Background()

	keyDefault, _, err := m.ComputeTargetKey(ctx, ".", "build", defaultCharms)
	require.NoError(t, err, "ComputeTargetKey (default charms)")
	keyBare, _, err := m.ComputeTargetKey(ctx, ".", "build", nil)
	require.NoError(t, err, "ComputeTargetKey (bare)")
	require.NotEqual(t, keyDefault, keyBare, "test setup: charm variants should key differently")

	matchesDefault, err := m.IdentifyRef(ctx, cache.PortableRef(keyDefault))
	require.NoError(t, err, "IdentifyRef (default-charm ref)")
	require.Len(t, matchesDefault, 1, "IdentifyRef: expected exactly one match for the default-charm ref")
	assert.Equal(t, types.RefMatch{Project: ".", Target: "build", Charms: []string{"rw"}}, matchesDefault[0])

	matchesBare, err := m.IdentifyRef(ctx, cache.PortableRef(keyBare))
	require.NoError(t, err, "IdentifyRef (bare ref)")
	require.Len(t, matchesBare, 1, "IdentifyRef: expected exactly one match for the bare (CI) ref")
	assert.Equal(t, types.RefMatch{Project: ".", Target: "build", Charms: nil}, matchesBare[0])
}

// TestIdentifyRef_InvalidRefShape guards the input-validation path: a string that
// is not even shaped like a ref (no "out" + hex tail) can match nothing by
// construction, so it must short-circuit to an empty slice without sweeping.
func TestIdentifyRef_InvalidRefShape(t *testing.T) {
	m := newIdentityWorkspace(t)
	ctx := context.Background()

	for _, ref := range []string{"", "not-a-ref", "refactor"} {
		matches, err := m.IdentifyRef(ctx, ref)
		require.NoErrorf(t, err, "IdentifyRef(%q)", ref)
		assert.Emptyf(t, matches, "IdentifyRef(%q): expected no matches", ref)
	}
}

// TestIdentifyRef_NoCachePropagatesError guards the one case where a target's
// keying failure must NOT be swallowed by the best-effort sweep: a cache-free
// (Inspect) workspace can mint no keys at all, so ComputeTargetKey's
// types.ErrNoCache must propagate rather than silently resolving to "no matches".
func TestIdentifyRef_NoCachePropagatesError(t *testing.T) {
	const spellName = "zzz-identify-nocache-spell"
	s := spells.NewSpell(spellName, spells.WithTargets("build"))
	project.DefaultSpellRegistry().RegisterSpell(s)
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(spellName) })

	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(""), 0o644))

	reg := NewWorkspaceRegistry()
	reg.RegisterProject(".", WithSpell(spellName))
	m, err := inspect(context.Background(), root, WithWorkspaceRegistry(reg))
	require.NoError(t, err, "inspect")
	require.NoError(t, m.load(context.Background()), "load")

	_, err = m.IdentifyRef(context.Background(), "out123456789012")
	assert.ErrorIs(t, err, types.ErrNoCache, "IdentifyRef on a cache-free workspace")
}

// TestRefMatchCommand covers the renderings cmd/magus/query.go's ref-lookup
// suggestion and internal/handler/mcp's magus_output not-found fallback both rely
// on: root-project omission, a charm suffix on a match that required explicit
// charms, and --no-default-charms on a bare match in a workspace with configured
// defaults, read from m.cfg.DefaultCharms rather than passed in.
func TestRefMatchCommand(t *testing.T) {
	cases := []struct {
		name          string
		mt            types.RefMatch
		defaultCharms []string
		want          string
	}{
		{
			name: "root project omitted",
			mt:   types.RefMatch{Project: ".", Target: "build"},
			want: "magus run build",
		},
		{
			name: "non-root project named",
			mt:   types.RefMatch{Project: "pkg/a", Target: "build"},
			want: "magus run build pkg/a",
		},
		{
			name: "explicit charms suffix, no --no-default-charms",
			mt:   types.RefMatch{Project: ".", Target: "build", Charms: []string{"rw"}},
			want: "magus run build:rw",
		},
		{
			name:          "bare match with configured defaults gets --no-default-charms",
			mt:            types.RefMatch{Project: ".", Target: "build"},
			defaultCharms: []string{"rw"},
			want:          "magus run build --no-default-charms",
		},
		{
			name: "bare match with no configured defaults",
			mt:   types.RefMatch{Project: ".", Target: "build"},
			want: "magus run build",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := newIdentityWorkspace(t, c.defaultCharms...)
			if got := m.RefMatchCommand(c.mt); got != c.want {
				t.Errorf("RefMatchCommand(%+v) = %q, want %q", c.mt, got, c.want)
			}
		})
	}
}
