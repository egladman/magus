package magus

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCacheOperationsWithoutOpenCache(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), nil, 0o644))
	m, err := Inspect(context.Background(), root)
	require.NoError(t, err)
	workspace := m.(*Magus)

	tier, mode := workspace.CacheDescription()
	assert.Empty(t, tier+mode, "an Inspect workspace has no cache to describe")
	_, _, err = workspace.PruneCache(context.Background(), time.Now(), false)
	assert.ErrorIs(t, err, types.ErrNoCache)
	assert.ErrorIs(t, workspace.PruneRemoteCache(context.Background(), time.Hour, 1, false), types.ErrNoCache)
	assert.ErrorIs(t, workspace.ExportCache(context.Background(), &strings.Builder{}), types.ErrNoCache)
	assert.ErrorIs(t, workspace.ImportCache(context.Background(), strings.NewReader("")), types.ErrNoCache)
	_, err = workspace.TailLog(".", "")
	assert.ErrorIs(t, err, types.ErrNoCache)

	stats := workspace.CacheStats()
	collector, enabled := workspace.MetricsCollector()
	metrics, err := workspace.MetricsSnapshot(context.Background())
	require.NoError(t, err)
	assert.Nil(t, collector)
	assert.False(t, enabled)
	assert.Equal(t, struct {
		Stats     CacheStats
		DiskBytes int64
		Metrics   []byte
		CacheDir  string
		Workspace string
	}{
		Stats:     CacheStats{},
		Metrics:   nil,
		CacheDir:  workspace.CacheDir(),
		Workspace: workspace.Root(),
	}, struct {
		Stats     CacheStats
		DiskBytes int64
		Metrics   []byte
		CacheDir  string
		Workspace string
	}{
		Stats:     stats,
		DiskBytes: workspace.CacheDiskBytes(),
		Metrics:   metrics,
		CacheDir:  workspace.CacheDir(),
		Workspace: workspace.Root(),
	})
}

func TestResolveCacheDir_DoesNotNeedWorkspaceLoad(t *testing.T) {
	root := t.TempDir()
	// No magusfile is needed: this deliberately resolves only config/cache placement, not projects.
	got, err := ResolveCacheDir(root)
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, ".magus"), got)

	got, err = ResolveCacheDir(root, WithLoadedConfig(config.Config{Cache: config.Cache{Dir: "cache"}}))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "cache"), got)
}

func TestCacheDirIsUnderTheWorkspaceRoot(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)
	assert.Equal(t, filepath.Join(m.Root(), ".magus"), m.CacheDir(),
		"the journal run logs and the per-ref output store hang off this one root")
}

// TestCacheStatsAndDiskBytesOnAnOpenWorkspace: a freshly opened cache has served
// nothing, and its on-disk size is a real (non-negative) figure rather than the
// zero an Inspect workspace reports for "no cache attached".
func TestCacheStatsAndDiskBytesOnAnOpenWorkspace(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)

	assert.Equal(t, CacheStats{}, m.CacheStats(), "nothing has been hit or missed yet")
	assert.GreaterOrEqual(t, m.CacheDiskBytes(), int64(0))
}

// TestExportImportCacheRoundTrip: Export writes the whole cache as a gzip tar and
// Import reads one back. An empty cache is the interesting case: it must produce a
// well-formed archive rather than nothing.
func TestExportImportCacheRoundTrip(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)
	ctx := context.Background()

	var buf bytes.Buffer
	require.NoError(t, m.ExportCache(ctx, &buf))
	assert.NotEmpty(t, buf.Bytes(), "an export is an archive, never an empty stream")

	require.NoError(t, m.ImportCache(ctx, bytes.NewReader(buf.Bytes())))
}

func TestPruneCacheDryRunRemovesNothing(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)

	removed, freed, err := m.PruneCache(context.Background(), time.Now(), true)
	require.NoError(t, err)
	assert.Zero(t, removed, "an empty cache has nothing to prune")
	assert.Zero(t, freed)
}

// TestPruneRemoteCacheNeedsABackend: pruning a remote is not a silent no-op when
// none is wired: "there is no remote" and "the remote had nothing" are different
// answers, and only one of them means the retention policy ran.
func TestPruneRemoteCacheNeedsABackend(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)
	assert.Error(t, m.PruneRemoteCache(context.Background(), time.Hour, 1, false))
}

// TestOutputLookupsOnAnEmptyStore: every ref-resolving accessor reports
// fs.ErrNotExist for a ref nothing produced. They read the output store straight
// off the resolved cache dir, so they answer on an Inspect workspace too.
func TestOutputLookupsOnAnEmptyStore(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)
	const ref = "outdeadbe"

	_, _, err := m.OutputByRef(ref)
	assert.ErrorIs(t, err, fs.ErrNotExist)

	_, err = m.OutputAttempts(ref)
	assert.ErrorIs(t, err, fs.ErrNotExist)

	_, err = m.OutputDescriptorByRef(ref)
	assert.ErrorIs(t, err, fs.ErrNotExist)

	_, err = m.OutputKeyInputs(ref)
	assert.ErrorIs(t, err, fs.ErrNotExist)

	// With a live cache the remote namespace is consulted too; with no backend
	// wired it still comes back as unknown rather than hanging or panicking.
	_, _, err = m.OutputByRefRemote(context.Background(), ref)
	assert.Error(t, err)
}

func TestInvocationLookupsOnAnEmptyJournal(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)

	inv, err := m.InvocationByID("inv-never-written")
	assert.ErrorIs(t, err, fs.ErrNotExist)
	assert.Equal(t, Invocation{}, inv)

	inv, events, err := m.InvocationEventsByID("inv-never-written")
	assert.ErrorIs(t, err, fs.ErrNotExist)
	assert.Equal(t, Invocation{}, inv)
	assert.Empty(t, events)
}

// TestTailLogWithNoEntries: a live cache that has run nothing wraps fs.ErrNotExist
// rather than reporting ErrNoCache, which is the Inspect-workspace answer.
func TestTailLogWithNoEntries(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)

	_, err := m.TailLog("api", "")
	require.Error(t, err)
	assert.NotErrorIs(t, err, types.ErrNoCache)

	_, err = m.TailLog("api", "build")
	require.Error(t, err)
	assert.NotErrorIs(t, err, types.ErrNoCache)
}

func TestListAndGetArtifactWithoutACache(t *testing.T) {
	root := writeWorkspace(t, map[string]string{"magusfile.buzz": ""})
	ws, err := Inspect(context.Background(), root)
	require.NoError(t, err)
	m := ws.(*Magus)

	_, err = m.ListArtifacts(context.Background(), "api", "api/gen/x.go")
	assert.ErrorIs(t, err, types.ErrNoCache)

	assert.ErrorIs(t, m.GetArtifact(context.Background(), cache.ArtifactVersion{}, filepath.Join(root, "dst")),
		types.ErrNoCache)

	_, err = m.PublishOutput(context.Background(), "outdeadbe")
	assert.ErrorIs(t, err, types.ErrNoCache)
}

// TestMetricsAreOffUnlessAskedFor is the CLI default: opening without
// WithMetricsCollection wires no in-process reader, so both accessors report
// absence rather than an empty snapshot a caller would mistake for real data.
func TestMetricsAreOffUnlessAskedFor(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)

	snapshot, err := m.MetricsSnapshot(context.Background())
	require.NoError(t, err)
	assert.Nil(t, snapshot)

	collector, ok := m.MetricsCollector()
	assert.False(t, ok)
	assert.Nil(t, collector)
}

// TestMetricsCollectionYieldsAReadableSnapshot covers the server's path: with
// collection on, both the OTLP bytes and the raw metricdata are available from the
// same instruments, with no exporter hop for the second.
func TestMetricsCollectionYieldsAReadableSnapshot(t *testing.T) {
	root := writeWorkspace(t, map[string]string{"magusfile.buzz": ""})

	m, err := Open(context.Background(), root, WithMetricsCollection())
	require.NoError(t, err)
	t.Cleanup(func() { _ = m.Close() })

	snapshot, err := m.MetricsSnapshot(context.Background())
	require.NoError(t, err)
	assert.NotNil(t, snapshot, "an enabled workspace exports OTLP protobuf")

	collector, ok := m.MetricsCollector()
	require.True(t, ok)
	require.NotNil(t, collector)

	_, err = collector.Collect(context.Background())
	assert.NoError(t, err)
}

// TestSecretProviderNamesTheSpellOnly. There is deliberately no accessor for the
// references a workspace can reach: a standing inventory of what a build can fetch
// is a map of what to go after.
func TestSecretProviderNamesTheSpellOnly(t *testing.T) {
	m, _ := openTempWorkspace(t, "api", nil)
	assert.Empty(t, m.SecretProvider(),
		"no provider spell is declared, so the built-in environment provider applies")
}

// TestNewOutputDescriptorCarriesEveryField is the projection that keeps an
// internal/ type out of the public surface. A field dropped here would silently
// vanish from `magus query output <ref> -o json`, which is why every one is set.
func TestNewOutputDescriptorCarriesEveryField(t *testing.T) {
	got := newOutputDescriptor(cache.OutputDescriptor{
		Ref: "outdeadbe", Project: "api", Target: "build", Inv: "inv1",
		Failed: true, ErrMsg: "exit 1", TimestampMs: 1700000000000, DurationMs: 1234,
		Key: "abc123", KeyVersion: 7, Attempt: "att1", MagusVersion: "v0.9.0",
		Revision: "cafebabe", Dirty: true,
		Spell: "go::go-build", ExtraArgs: []string{"-race"}, VCSName: "git", Platform: "darwin/arm64",
	})

	assert.Equal(t, OutputDescriptor{
		Ref: "outdeadbe", Project: "api", Target: "build", Inv: "inv1",
		Failed: true, ErrMsg: "exit 1", TimestampMs: 1700000000000, DurationMs: 1234,
		Key: "abc123", KeyVersion: 7, Attempt: "att1", MagusVersion: "v0.9.0",
		Revision: "cafebabe", Dirty: true,
		Spell: "go::go-build", ExtraArgs: []string{"-race"}, VCSName: "git", Platform: "darwin/arm64",
	}, got)

	assert.Equal(t, OutputDescriptor{}, newOutputDescriptor(cache.OutputDescriptor{}))
}

// TestResolveCacheDirReadsAnExplicitConfigFile covers WithConfigFile on the narrow
// path a sidecar writer takes: it must find the cache location without discovering
// projects or evaluating any magusfile.
func TestResolveCacheDirReadsAnExplicitConfigFile(t *testing.T) {
	root := writeWorkspace(t, map[string]string{"elsewhere/magus.yaml": "cache:\n  dir: cache-here\n"})

	got, err := ResolveCacheDir(root, WithConfigFile(filepath.Join(root, "elsewhere", "magus.yaml")))
	require.NoError(t, err)
	assert.Equal(t, filepath.Join(root, "cache-here"), got,
		"cache.dir is relative to the workspace root, not to the config file")
}
