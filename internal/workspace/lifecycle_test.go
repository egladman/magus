package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// stubLifecycleRunner installs fn as the runner for one test. The runner is package state
// the bindings layer sets at init, which this package's tests never link.
func stubLifecycleRunner(t *testing.T, fn LifecycleRunner) *int {
	t.Helper()
	calls := 0
	prev := lifecycleRunner
	lifecycleRunner = func(ctx context.Context, spellName, root string, keys []string) ([]spells.Lifecycle, error) {
		calls++
		return fn(ctx, spellName, root, keys)
	}
	t.Cleanup(func() { lifecycleRunner = prev })
	return &calls
}

var goAndNode = []spells.Lifecycle{
	{Key: "go", Source: "https://endoflife.date/api/v1/products/go", AsOf: "2026-09-24T07:44:41Z",
		Cycles: []spells.ReleaseCycle{{Cycle: "1.26"}}},
	{Key: "nodejs", Source: "https://endoflife.date/api/v1/products/nodejs", AsOf: "2026-09-20T00:00:00Z",
		Cycles: []spells.ReleaseCycle{{Cycle: "24", EOL: "2028-04-30", LTS: true}}},
}

func answering(got []spells.Lifecycle, err error) LifecycleRunner {
	return func(context.Context, string, string, []string) ([]spells.Lifecycle, error) { return got, err }
}

func TestAskLifecyclesUnwiredAsksNothing(t *testing.T) {
	calls := stubLifecycleRunner(t, answering(goAndNode, nil))
	a, err := AskLifecycles(t.Context(), ProviderCache{Dir: t.TempDir()}, t.TempDir(), "", []string{"go"}, nil)
	require.NoError(t, err)
	assert.Equal(t, types.LifecycleStatus{State: types.LifecycleUnwired}, a.Status)
	assert.Zero(t, *calls, "no provider wired means no network")
}

// A live answer is stored, and the stored copy is what doctor reads back: same records,
// state cached, and the fetch time of the live call rather than the read.
func TestAskLifecyclesStoresWhatDoctorReads(t *testing.T) {
	root, cacheDir := t.TempDir(), t.TempDir()
	stubLifecycleRunner(t, answering(goAndNode, nil))
	keys := []string{"nodejs", "go"}

	installed := []InstalledTool{{Project: ".", Bin: "go", Lifecycle: "go", Version: "v1.26.6"}}
	live, err := AskLifecycles(t.Context(), ProviderCache{Dir: cacheDir}, root, "endoflife-date", keys, installed)
	require.NoError(t, err)
	assert.Equal(t, types.LifecycleLive, live.Status.State)
	assert.Equal(t, "endoflife-date", live.Status.Provider)
	assert.Equal(t, []string{"https://endoflife.date/api/v1/products/go", "https://endoflife.date/api/v1/products/nodejs"}, live.Status.Sources)
	assert.Equal(t, "2026-09-20T00:00:00Z", live.Status.AsOf, "the report is no fresher than its stalest source")
	_, perr := time.Parse(time.RFC3339, live.Status.FetchedAt)
	require.NoError(t, perr)

	cached, ok := CachedLifecycles(t.Context(), cacheDir, root, "endoflife-date", []string{"go", "nodejs"})
	require.True(t, ok, "key order does not change the question")
	assert.Equal(t, types.LifecycleCached, cached.Status.State)
	assert.Equal(t, live.Status.FetchedAt, cached.Status.FetchedAt)
	assert.Equal(t, goAndNode, cached.Lifecycles)
	assert.Equal(t, installed, cached.Installed, "doctor places installed versions without a probe")

	_, ok = CachedLifecycles(t.Context(), cacheDir, root, "endoflife-date", []string{"go", "nodejs", "python"})
	assert.False(t, ok, "an answer to a different set of keys does not answer this one")
	_, ok = CachedLifecycles(t.Context(), cacheDir, root, "other-provider", keys)
	assert.False(t, ok, "another provider's name is another question")
}

// The spell's own source decides the answer as surely as the keys do, so editing a
// workspace spell misses rather than replaying what the old body said.
func TestLifecycleCacheMissesWhenAWorkspaceSpellChanges(t *testing.T) {
	root, cacheDir := t.TempDir(), t.TempDir()
	src := filepath.Join(root, "spells", "endoflife-date", "spell.buzz")
	require.NoError(t, os.MkdirAll(filepath.Dir(src), 0o755))
	require.NoError(t, os.WriteFile(src, []byte("// v1\n"), 0o644))
	stubLifecycleRunner(t, answering(goAndNode, nil))

	_, err := AskLifecycles(t.Context(), ProviderCache{Dir: cacheDir}, root, "endoflife-date", []string{"go"}, nil)
	require.NoError(t, err)
	_, ok := CachedLifecycles(t.Context(), cacheDir, root, "endoflife-date", []string{"go"})
	require.True(t, ok)

	require.NoError(t, os.WriteFile(src, []byte("// v2, a longer body\n"), 0o644))
	_, ok = CachedLifecycles(t.Context(), cacheDir, root, "endoflife-date", []string{"go"})
	assert.False(t, ok)
}

// Offline and unreached are states, never errors, and each replays the stored answer when
// there is one, labeled with when it was fetched.
func TestAskLifecyclesReplaysWhenTheProviderIsNotAsked(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		state string
	}{
		{"offline", ErrLifecycleOffline, types.LifecycleOffline},
		{"unreached", errors.Join(ErrLifecycleUnreached, errors.New("dial tcp: no route to host")), types.LifecycleUnreached},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, cacheDir := t.TempDir(), t.TempDir()
			cache := ProviderCache{Dir: cacheDir}
			keys := []string{"go"}

			stubLifecycleRunner(t, answering(nil, tc.err))
			none, err := AskLifecycles(t.Context(), cache, root, "endoflife-date", keys, nil)
			require.NoError(t, err, "not asking is not a failure")
			assert.Equal(t, tc.state, none.Status.State)
			assert.Empty(t, none.Lifecycles)
			assert.Empty(t, none.Status.FetchedAt, "nothing stored, so nothing to date")
			assert.Equal(t, tc.err.Error(), none.Status.Detail)

			stubLifecycleRunner(t, answering(goAndNode[:1], nil))
			live, err := AskLifecycles(t.Context(), cache, root, "endoflife-date", keys, nil)
			require.NoError(t, err)

			stubLifecycleRunner(t, answering(nil, tc.err))
			replayed, err := AskLifecycles(t.Context(), cache, root, "endoflife-date", keys, nil)
			require.NoError(t, err)
			assert.Equal(t, tc.state, replayed.Status.State)
			assert.Equal(t, live.Status.FetchedAt, replayed.Status.FetchedAt)
			assert.Equal(t, goAndNode[:1], replayed.Lifecycles)
		})
	}
}

func TestAskLifecyclesReturnsAMalformedAnswer(t *testing.T) {
	bad := errors.New(`spell "endoflife-date": list_lifecycles key "go": field "asOf" is "", want RFC 3339`)
	stubLifecycleRunner(t, answering(nil, bad))
	_, err := AskLifecycles(t.Context(), ProviderCache{Dir: t.TempDir()}, t.TempDir(), "endoflife-date", []string{"go"}, nil)
	require.ErrorIs(t, err, bad)
}

func TestAskLifecyclesNeverWritesAnImmutableCache(t *testing.T) {
	root, cacheDir := t.TempDir(), t.TempDir()
	stubLifecycleRunner(t, answering(goAndNode, nil))
	_, err := AskLifecycles(t.Context(), ProviderCache{Dir: cacheDir, Immutable: true}, root, "endoflife-date", []string{"go"}, nil)
	require.NoError(t, err)
	_, statErr := os.Stat(lifecycleCachePath(cacheDir, "endoflife-date"))
	assert.ErrorIs(t, statErr, os.ErrNotExist)
}

// With no product to ask about there is nothing to fetch; the provider is not called.
func TestAskLifecyclesWithNoKeysAsksNothing(t *testing.T) {
	calls := stubLifecycleRunner(t, answering(goAndNode, nil))
	a, err := AskLifecycles(t.Context(), ProviderCache{}, t.TempDir(), "endoflife-date", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, types.LifecycleStatus{Provider: "endoflife-date", State: types.LifecycleLive}, a.Status)
	assert.Zero(t, *calls)
}

func TestLifecycleKeysAreDistinctAndSorted(t *testing.T) {
	sp := spells.NewSpell("go", spells.WithTools(map[string]spells.Tool{"go": {Lifecycle: "go"}, "gofmt": {Lifecycle: "go"}}))
	node := spells.NewSpell("typescript", spells.WithTools(map[string]spells.Tool{"node": {Lifecycle: "nodejs"}, "tsc": {}}))
	projects := []*types.Project{
		{Path: "a", ResolvedSpells: []*spells.Spell{sp, node}},
		{Path: "b", ResolvedSpells: []*spells.Spell{sp}},
	}
	assert.Equal(t, []string{"go", "nodejs"}, LifecycleKeys(projects))
}
