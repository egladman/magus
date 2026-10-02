package knowledge

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fastRead is what one Ensure call asked of its callbacks: how often each ran, and the
// classes gather was handed (nil means gather never ran).
type fastRead struct {
	stamps, indexes int
	gathered        []ShardClass
}

// fastReadOptions is how a test describes the read: the cheap stamps it computes, the full
// stamps and declared indexes the deferred callbacks would return, and whether it forces a
// rebuild.
type fastReadOptions struct {
	fast    Stamps
	full    Stamps
	indexes []SymbolIndexDeclaration
	refresh bool
}

// seedFast writes a store whose classes carry the full stamps "v1" and the fast stamps
// "f1", the state a previous read left behind, and returns the cache dir with the inputs
// that built it. It declares no indexes, as a store written before they were recorded.
func seedFast(t *testing.T) (string, Inputs) {
	t.Helper()
	cacheDir, in := stampFixture(t)
	_, err := Ensure(context.Background(), cacheDir, BuildOptions{Stamps: stampsAll("v1"), FastStamps: stampsAll("f1")}, AllClasses,
		func([]ShardClass) (Inputs, error) { return in, nil }, nil)
	require.NoError(t, err)
	return cacheDir, in
}

// ensureFast runs Ensure the way the CLI does: FastStamps as a value, the full stamps and
// the declared indexes behind callbacks that count their calls, so a test can assert what a
// read paid for as well as what it returned.
func ensureFast(t *testing.T, cacheDir string, o fastReadOptions, want []ShardClass, in Inputs) (*Graph, *fastRead) {
	t.Helper()
	calls := &fastRead{}
	opts := BuildOptions{
		FastStamps: o.fast,
		Refresh:    o.refresh,
		StampsFunc: func(context.Context) (Stamps, error) {
			calls.stamps++
			return o.full, nil
		},
		IndexesFunc: func(context.Context) ([]SymbolIndexDeclaration, error) {
			calls.indexes++
			return o.indexes, nil
		},
	}
	g, err := Ensure(context.Background(), cacheDir, opts, want, func(stale []ShardClass) (Inputs, error) {
		calls.gathered = stale
		return in, nil
	}, nil)
	require.NoError(t, err)
	return g, calls
}

func declsFixture() []SymbolIndexDeclaration {
	return []SymbolIndexDeclaration{{Project: "pkg/a", Dir: "/ws/pkg/a", Language: "go", Path: "/ws/pkg/a/index.scip"}}
}

func TestEnsureFastStampsMatchingTheManifestPayForNothing(t *testing.T) {
	cacheDir, in := seedFast(t)
	built, _ := ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)
	before := readManifest(t, cacheDir)

	loaded, calls := ensureFast(t, cacheDir, fastReadOptions{
		fast: stampsAll("f1"), full: stampsAll("never"), indexes: declsFixture(),
	}, DefaultClasses, Inputs{})

	assert.Zero(t, calls.stamps, "the whole point of the fast stamp: the model is never evaluated")
	assert.Zero(t, calls.indexes, "a read the fast stamps settle never evaluates the workspace to record indexes")
	assert.Nil(t, calls.gathered)
	assert.Equal(t, outputJSON(t, built), outputJSON(t, loaded), "the stored graph is the assembled one")
	assert.Equal(t, before, readManifest(t, cacheDir), "a settled read leaves the manifest alone")
}

func TestEnsureFastMissWithMatchingFullStampRecordsFastAndIndexesWithoutRebuilding(t *testing.T) {
	cacheDir, in := seedFast(t)
	before := readManifest(t, cacheDir)
	require.False(t, before.IndexesKnown, "a store written without a declaration never recorded them")

	_, calls := ensureFast(t, cacheDir, fastReadOptions{
		fast: stampsAll("f2"), full: stampsAll("v1"), indexes: declsFixture(),
	}, DefaultClasses, in)

	assert.Equal(t, 1, calls.stamps, "an unsettled class is judged by the full stamps, computed once")
	assert.Equal(t, 1, calls.indexes, "the workspace was paid for, so the declarations ride along")
	assert.Nil(t, calls.gathered, "the full stamp matched: nothing is reassembled")
	after := readManifest(t, cacheDir)
	for _, c := range DefaultClasses {
		assert.Equalf(t, "f2"+string(c), after.FastStamps[c], "class %s records the fast stamp that just missed", c)
	}
	for _, c := range LazyClasses {
		assert.Equalf(t, "f1"+string(c), after.FastStamps[c], "class %s was not wanted, so its record stays", c)
	}
	assert.Equal(t, before.Inputs, after.Inputs, "the full stamps are what settled the read, never rewritten")
	assert.Equal(t, before.Shards, after.Shards, "no shard moves")
	assert.Equal(t, declsFixture(), after.Indexes)
	assert.True(t, after.IndexesKnown)

	_, calls = ensureFast(t, cacheDir, fastReadOptions{
		fast: stampsAll("f2"), full: stampsAll("never"), indexes: declsFixture(),
	}, DefaultClasses, Inputs{})
	assert.Zero(t, calls.stamps, "the repaired store settles the next read cheaply")
}

func TestEnsureFastMissRecordsAnEmptyIndexListAsKnown(t *testing.T) {
	cacheDir, in := seedFast(t)

	_, calls := ensureFast(t, cacheDir, fastReadOptions{
		fast: stampsAll("f2"), full: stampsAll("v1"), indexes: nil,
	}, DefaultClasses, in)

	assert.Equal(t, 1, calls.indexes)
	after := readManifest(t, cacheDir)
	assert.True(t, after.IndexesKnown, "a workspace that declares no index is an answer, not a gap")
	assert.Empty(t, after.Indexes)
}

func TestEnsureFastMissKeepsIndexesAlreadyRecorded(t *testing.T) {
	cacheDir, in := seedFast(t)
	ensureFast(t, cacheDir, fastReadOptions{fast: stampsAll("f2"), full: stampsAll("v1"), indexes: declsFixture()}, DefaultClasses, in)

	_, calls := ensureFast(t, cacheDir, fastReadOptions{
		fast: stampsAll("f3"), full: stampsAll("v1"), indexes: []SymbolIndexDeclaration{{Project: "other"}},
	}, DefaultClasses, in)

	assert.Equal(t, 1, calls.stamps)
	assert.Zero(t, calls.indexes, "a manifest that knows its indexes does not ask again")
	after := readManifest(t, cacheDir)
	assert.Equal(t, declsFixture(), after.Indexes)
	assert.Equal(t, "f3"+string(ClassDomain), after.FastStamps[ClassDomain], "the fast stamp still moves")
}

func TestEnsureFastMissWithMissingFullStampRebuildsAndRecordsInputsFastAndIndexes(t *testing.T) {
	cacheDir, in := seedFast(t)
	full := stampsAll("v1")
	full[ClassRuntime] = "v2runtime"
	in.Indexes = declsFixture()

	_, calls := ensureFast(t, cacheDir, fastReadOptions{fast: stampsAll("f2"), full: full}, DefaultClasses, in)

	assert.Equal(t, 1, calls.stamps, "the stamps asked for before the verdict are still asked for once")
	assert.Equal(t, []ShardClass{ClassRuntime}, calls.gathered, "only the class whose full stamp moved is reassembled")
	after := readManifest(t, cacheDir)
	assert.Equal(t, "v2runtime", after.Inputs[ClassRuntime])
	assert.Equal(t, "f2runtime", after.FastStamps[ClassRuntime], "a rebuilt class is stamped with both stamps")
	assert.Equal(t, declsFixture(), after.Indexes, "a rebuild records what gather resolved")
	assert.True(t, after.IndexesKnown)
}

func TestEnsureFastMissWithoutAFastStampFallsBackToTheFullStamps(t *testing.T) {
	cacheDir, in := seedFast(t)
	fast := stampsAll("f1")
	fast[ClassDomain] = ""

	_, calls := ensureFast(t, cacheDir, fastReadOptions{fast: fast, full: stampsAll("v1")}, DefaultClasses, in)

	assert.Equal(t, 1, calls.stamps, "a stamp the caller could not compute never counts as a match")
	assert.Nil(t, calls.gathered)
	assert.Equal(t, "f1"+string(ClassDomain), readManifest(t, cacheDir).FastStamps[ClassDomain],
		"and is never recorded as one either")
}

func TestEnsureFastFreshClassWhoseShardWentBadResolvesTheFullStampsToRebuild(t *testing.T) {
	cacheDir, in := seedFast(t)
	path := filepath.Join(StoreDir(cacheDir), "shards", shardSlug("pkg/a")+".json")
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	var sf shardFile
	require.NoError(t, json.Unmarshal(b, &sf))
	sf.Fingerprint = "written-by-another-build"
	b, err = json.Marshal(sf)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, b, 0o644))

	_, calls := ensureFast(t, cacheDir, fastReadOptions{fast: stampsAll("f1"), full: stampsAll("v1")}, DefaultClasses, in)

	assert.Equal(t, 1, calls.stamps, "a rebuilt class is stamped with the full stamps, so the fast verdict alone is not enough")
	assert.Equal(t, []ShardClass{ClassDomain}, calls.gathered)
}

func TestEnsureRefreshIgnoresFastStamps(t *testing.T) {
	cacheDir, in := seedFast(t)

	_, calls := ensureFast(t, cacheDir, fastReadOptions{
		fast: stampsAll("f1"), full: stampsAll("v1"), refresh: true,
	}, AllClasses, in)

	assert.Equal(t, 1, calls.stamps, "a matching fast stamp must not settle a forced rebuild")
	assert.ElementsMatch(t, AllClasses, calls.gathered)
}

func TestEnsureFastStampsWithNoManifestFallToTheFullStamps(t *testing.T) {
	cacheDir, in := stampFixture(t)

	_, calls := ensureFast(t, cacheDir, fastReadOptions{fast: stampsAll("f1"), full: stampsAll("v1")}, AllClasses, in)

	assert.Equal(t, 1, calls.stamps)
	assert.ElementsMatch(t, AllClasses, calls.gathered)
	assert.Equal(t, "f1"+string(ClassDomain), readManifest(t, cacheDir).FastStamps[ClassDomain], "the first build records the fast stamp")
}

func TestEnsurePropagatesAStampsFuncError(t *testing.T) {
	cacheDir, in := seedFast(t)
	boom := errors.New("evaluating the workspace failed")

	_, err := Ensure(context.Background(), cacheDir, BuildOptions{
		FastStamps: stampsAll("f2"),
		StampsFunc: func(context.Context) (Stamps, error) { return nil, boom },
	}, DefaultClasses, func([]ShardClass) (Inputs, error) { return in, nil }, nil)

	assert.ErrorIs(t, err, boom)
}

func TestSymbolIndexDeclarationsAnswerOnlyForTheRecordedDomainFastStamp(t *testing.T) {
	cacheDir, in := seedFast(t)
	store := NewStore(cacheDir, true, 0, nil, nil)

	_, ok := store.SymbolIndexDeclarations("f1" + string(ClassDomain))
	assert.False(t, ok, "a store that never recorded indexes cannot vouch for them, whatever the stamp")

	ensureFast(t, cacheDir, fastReadOptions{fast: stampsAll("f2"), full: stampsAll("v1"), indexes: declsFixture()}, DefaultClasses, in)
	domain := "f2" + string(ClassDomain)

	got, ok := store.SymbolIndexDeclarations(domain)
	require.True(t, ok)
	assert.Equal(t, declsFixture(), got)

	_, ok = store.SymbolIndexDeclarations("f1" + string(ClassDomain))
	assert.False(t, ok, "the stamp the declarations were recorded under has moved on")
	_, ok = store.SymbolIndexDeclarations("f2" + string(ClassRuntime))
	assert.False(t, ok, "only the domain stamp vouches for a workspace's indexes")
	_, ok = store.SymbolIndexDeclarations("")
	assert.False(t, ok, "a stamp the caller could not compute matches nothing")

	got[0].Path = "mutated"
	again, ok := store.SymbolIndexDeclarations(domain)
	require.True(t, ok)
	assert.Equal(t, declsFixture(), again, "the caller gets a copy, not the store's slice")
}

func TestSymbolIndexDeclarationsAnEmptyListRecordedCountsAsKnown(t *testing.T) {
	cacheDir, in := seedFast(t)
	ensureFast(t, cacheDir, fastReadOptions{fast: stampsAll("f2"), full: stampsAll("v1")}, DefaultClasses, in)

	got, ok := NewStore(cacheDir, true, 0, nil, nil).SymbolIndexDeclarations("f2" + string(ClassDomain))

	assert.True(t, ok, "a workspace that declares no index answers yes, with nothing")
	assert.Empty(t, got)
}

func TestSymbolIndexDeclarationsWithoutAManifestAnswerNothing(t *testing.T) {
	cacheDir, _ := stampFixture(t)

	_, ok := NewStore(cacheDir, true, 0, nil, nil).SymbolIndexDeclarations("f1" + string(ClassDomain))

	assert.False(t, ok)
}

// A sync that rebuilds one class must carry every other class's record forward; Extra is
// what the next stamp folds for a class this sync did not touch, whether or not the sync
// declares indexes.
func TestStoreSyncKeepsUntouchedClassesExtra(t *testing.T) {
	cacheDir, in := stampFixture(t)
	in.Extra = map[ShardClass][]string{ClassDomain: {"/ws/magus.yaml"}}
	_, err := Ensure(context.Background(), cacheDir, BuildOptions{Stamps: stampsAll("v1")}, AllClasses,
		func([]ShardClass) (Inputs, error) { return in, nil }, nil)
	require.NoError(t, err)
	store := NewStore(cacheDir, true, 0, nil, nil)
	require.Equal(t, []string{"/ws/magus.yaml"}, store.ExtraInputs(ClassDomain))

	stamps := stampsAll("v1")
	stamps[ClassRuntime] = "v2runtime"
	in.Extra = nil
	ensure(t, cacheDir, stamps, DefaultClasses, in)

	assert.Equal(t, []string{"/ws/magus.yaml"}, store.ExtraInputs(ClassDomain), "the domain class was not rebuilt")
}

// The CLI's gather always declares indexes (never nil), including on the very first build
// of a store, when there is no manifest to carry anything forward from.
func TestStoreFirstSyncRecordsDeclaredIndexes(t *testing.T) {
	cacheDir, in := stampFixture(t)
	in.Indexes = declsFixture()

	ensure(t, cacheDir, stampsAll("v1"), AllClasses, in)

	man := readManifest(t, cacheDir)
	assert.Equal(t, declsFixture(), man.Indexes)
	assert.True(t, man.IndexesKnown)
}
