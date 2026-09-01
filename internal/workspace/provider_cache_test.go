package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/project"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// registerProviderSpell registers a stand-in provider spell whose Sources are the
// invalidation globs the cache fingerprints, and unregisters it after the test.
func registerProviderSpell(t *testing.T, name string, globs ...string) {
	t.Helper()
	project.DefaultSpellRegistry().RegisterSpell(spells.NewSpell(name, spells.WithSources(globs...)))
	t.Cleanup(func() { project.DefaultSpellRegistry().UnregisterSpell(name) })
}

// countingRunner returns a runner that reports one project and counts its calls.
func countingRunner(t *testing.T, calls *int) ProviderRunner {
	t.Helper()
	return func(context.Context, string, string) ([]spells.ProvidedProject, error) {
		*calls++
		return []spells.ProvidedProject{{Path: "libs/foo"}}, nil
	}
}

func TestProviderCacheReplaysUntilAnInputChanges(t *testing.T) {
	registerProviderSpell(t, "nx-cache", "nx.json", "**/project.json")
	ws := newWorkspace(t, []string{"libs/foo"})
	require.NoError(t, os.WriteFile(filepath.Join(ws.Root, "nx.json"), []byte(`{}`), 0o644))
	cacheDir := t.TempDir()
	calls := 0
	withRunner(t, countingRunner(t, &calls))

	for range 3 {
		ws.Projects = map[string]*types.Project{}
		require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"nx-cache"}, ProviderCache{Dir: cacheDir}))
		require.NotNil(t, ws.Projects["libs/foo"], "a replayed answer must land as a real project")
	}
	assert.Equal(t, 1, calls, "an unchanged workspace must not shell out to the foreign tool again")

	// A declaring file changing is the whole point of the fingerprint.
	require.NoError(t, os.WriteFile(filepath.Join(ws.Root, "libs", "foo", "project.json"), []byte(`{}`), 0o644))
	ws.Projects = map[string]*types.Project{}
	require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"nx-cache"}, ProviderCache{Dir: cacheDir}))
	assert.Equal(t, 2, calls, "a new project.json must re-run the provider")
}

func TestProviderCacheIgnoresUndeclaredFiles(t *testing.T) {
	registerProviderSpell(t, "nx-narrow", "nx.json")
	ws := newWorkspace(t, []string{"libs/foo"})
	require.NoError(t, os.WriteFile(filepath.Join(ws.Root, "nx.json"), []byte(`{}`), 0o644))
	cacheDir := t.TempDir()
	calls := 0
	withRunner(t, countingRunner(t, &calls))

	require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"nx-narrow"}, ProviderCache{Dir: cacheDir}))
	require.NoError(t, os.WriteFile(filepath.Join(ws.Root, "libs", "foo", "index.ts"), []byte("x"), 0o644))
	ws.Projects = map[string]*types.Project{}
	require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"nx-narrow"}, ProviderCache{Dir: cacheDir}))

	assert.Equal(t, 1, calls, "editing source the provider never declared must not re-derive the project set")
}

func TestProviderCacheDisabledWithoutGlobs(t *testing.T) {
	registerProviderSpell(t, "nx-noglobs")
	ws := newWorkspace(t, []string{"libs/foo"})
	cacheDir := t.TempDir()
	calls := 0
	withRunner(t, countingRunner(t, &calls))

	for range 2 {
		ws.Projects = map[string]*types.Project{}
		require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"nx-noglobs"}, ProviderCache{Dir: cacheDir}))
	}
	assert.Equal(t, 2, calls, "with nothing declared to invalidate against, correctness means running every time")
	assert.NoFileExists(t, providerCachePath(cacheDir, "nx-noglobs"))
}

func TestProviderCacheDisabledWithoutCacheDir(t *testing.T) {
	registerProviderSpell(t, "nx-nocachedir", "nx.json")
	ws := newWorkspace(t, []string{"libs/foo"})
	calls := 0
	withRunner(t, countingRunner(t, &calls))

	for range 2 {
		ws.Projects = map[string]*types.Project{}
		require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"nx-nocachedir"}, ProviderCache{}))
	}
	assert.Equal(t, 2, calls)
}

func TestProviderCacheSurvivesACorruptEntry(t *testing.T) {
	registerProviderSpell(t, "nx-corrupt", "nx.json")
	ws := newWorkspace(t, []string{"libs/foo"})
	require.NoError(t, os.WriteFile(filepath.Join(ws.Root, "nx.json"), []byte(`{}`), 0o644))
	cacheDir := t.TempDir()
	path := providerCachePath(cacheDir, "nx-corrupt")
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
	require.NoError(t, os.WriteFile(path, []byte("not json"), 0o644))
	calls := 0
	withRunner(t, countingRunner(t, &calls))

	require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"nx-corrupt"}, ProviderCache{Dir: cacheDir}),
		"the provider is the source of truth, so an unreadable cache costs time, not correctness")
	assert.Equal(t, 1, calls)
	assert.NotNil(t, ws.Projects["libs/foo"])
}

func TestProviderCacheRoundTripsEveryField(t *testing.T) {
	registerProviderSpell(t, "nx-fields", "nx.json")
	ws := newWorkspace(t, []string{"libs/foo", "libs/shared"})
	require.NoError(t, os.WriteFile(filepath.Join(ws.Root, "nx.json"), []byte(`{}`), 0o644))
	cacheDir := t.TempDir()
	want := spells.ProvidedProject{
		Path:      "libs/foo",
		Name:      "@acme/foo",
		Spells:    nil, // a spell name would have to be registered to bind; covered elsewhere
		DependsOn: []string{"libs/shared"},
		Sources:   []string{"**/*.ts"},
		Outputs:   []string{"dist/**"},
		Exclusive: true,
	}
	calls := 0
	withRunner(t, func(context.Context, string, string) ([]spells.ProvidedProject, error) {
		calls++
		return []spells.ProvidedProject{want}, nil
	})

	require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"nx-fields"}, ProviderCache{Dir: cacheDir}))
	fromRunner := *ws.Projects["libs/foo"]

	ws.Projects = map[string]*types.Project{}
	require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"nx-fields"}, ProviderCache{Dir: cacheDir}))
	fromCache := *ws.Projects["libs/foo"]

	require.Equal(t, 1, calls, "the second open must have replayed")
	assert.Equal(t, fromRunner, fromCache, "a replayed project must be identical to a freshly derived one")
}

func TestProviderCachePathSanitizesTheSpellName(t *testing.T) {
	// The name reaches here from a magusfile, so it must not be able to escape the
	// cache directory.
	got := providerCachePath("/cache", "../../etc/passwd")
	assert.Equal(t, filepath.Join("/cache", "providers", "______etc_passwd.json"), got)
}

// Sanitizing is what makes two names share a file, so the fingerprint has to be what
// tells them apart: with the same declared globs in the same root, the pair agreed on a
// fingerprint and each replayed the other's project set out of the shared entry.
func TestProviderCacheDistinguishesNamesThatShareAFile(t *testing.T) {
	require.Equal(t, providerCachePath("/cache", "my/nx"), providerCachePath("/cache", "my_nx"),
		"the premise: these two names sanitize to one file")

	registerProviderSpell(t, "my/nx", "nx.json")
	registerProviderSpell(t, "my_nx", "nx.json")
	ws := newWorkspace(t, []string{"libs/foo"})
	require.NoError(t, os.WriteFile(filepath.Join(ws.Root, "nx.json"), []byte(`{}`), 0o644))
	cacheDir := t.TempDir()
	calls := 0
	withRunner(t, countingRunner(t, &calls))

	require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"my/nx"}, ProviderCache{Dir: cacheDir}))
	ws.Projects = map[string]*types.Project{}
	require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"my_nx"}, ProviderCache{Dir: cacheDir}))

	assert.Equal(t, 2, calls, "one provider's answer must not replay for a differently-named one")
}

func TestProviderCacheRespectsImmutable(t *testing.T) {
	registerProviderSpell(t, "nx-immutable", "nx.json")
	ws := newWorkspace(t, []string{"libs/foo"})
	require.NoError(t, os.WriteFile(filepath.Join(ws.Root, "nx.json"), []byte(`{}`), 0o644))
	cacheDir := t.TempDir()
	calls := 0
	withRunner(t, countingRunner(t, &calls))

	for range 2 {
		ws.Projects = map[string]*types.Project{}
		require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"nx-immutable"},
			ProviderCache{Dir: cacheDir, Immutable: true}))
	}

	assert.Equal(t, 2, calls, "an immutable cache may be read but never written, so every open re-derives")
	assert.NoFileExists(t, providerCachePath(cacheDir, "nx-immutable"))
}

func TestProviderCacheRefusesAnEmptyAnswer(t *testing.T) {
	registerProviderSpell(t, "nx-empty", "nx.json")
	ws := newWorkspace(t, []string{"libs/foo"})
	require.NoError(t, os.WriteFile(filepath.Join(ws.Root, "nx.json"), []byte(`{}`), 0o644))
	cacheDir := t.TempDir()
	calls := 0
	withRunner(t, func(context.Context, string, string) ([]spells.ProvidedProject, error) {
		calls++
		return nil, nil
	})

	for range 2 {
		require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"nx-empty"}, ProviderCache{Dir: cacheDir}))
	}

	assert.Equal(t, 2, calls,
		"reporting nothing usually means the tool is not installed yet; installing it touches only pruned dirs, so a remembered empty answer would never invalidate")
	assert.NoFileExists(t, providerCachePath(cacheDir, "nx-empty"))
}

func TestProviderCacheRefusesUncacheableGlobs(t *testing.T) {
	for _, tc := range []struct {
		name string
		glob string
	}{
		{"malformed", "libs/[foo/**"},
		// Nothing matches: the declared file is missing, so a digest over it would be a
		// constant that no edit could ever move.
		{"matching nothing", "nowhere.json"},
		// Declared inside a pruned directory, which the walk never descends into.
		{"inside a pruned dir", "node_modules/**/project.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			name := "nx-" + strings.ReplaceAll(tc.name, " ", "-")
			registerProviderSpell(t, name, tc.glob)
			ws := newWorkspace(t, []string{"libs/foo", "node_modules/pkg"})
			require.NoError(t, os.WriteFile(filepath.Join(ws.Root, "node_modules", "pkg", "project.json"), []byte(`{}`), 0o644))
			cacheDir := t.TempDir()
			calls := 0
			withRunner(t, countingRunner(t, &calls))

			for range 2 {
				ws.Projects = map[string]*types.Project{}
				require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{name}, ProviderCache{Dir: cacheDir}))
			}

			assert.Equal(t, 2, calls, "a fingerprint that cannot change must disable the cache, not pin one answer")
			assert.NoFileExists(t, providerCachePath(cacheDir, name))
		})
	}
}

func TestProviderCacheKeyedByWorkspaceRoot(t *testing.T) {
	// Two workspaces may share one absolute cache dir (MAGUS_CACHE_DIR), and both may
	// wire a spell of the same name.
	registerProviderSpell(t, "nx-shared", "nx.json")
	cacheDir := t.TempDir()
	var roots []string
	calls := 0
	withRunner(t, countingRunner(t, &calls))

	for range 2 {
		ws := newWorkspace(t, []string{"libs/foo"})
		require.NoError(t, os.WriteFile(filepath.Join(ws.Root, "nx.json"), []byte(`{}`), 0o644))
		roots = append(roots, ws.Root)
		require.NoError(t, AddProvidedProjects(context.Background(), ws, []string{"nx-shared"}, ProviderCache{Dir: cacheDir}))
	}

	require.NotEqual(t, roots[0], roots[1])
	assert.Equal(t, 2, calls, "one workspace's answer must not replay for another")
}
