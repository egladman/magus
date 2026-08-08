package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
)

// withRunner installs fn as the provider runner for one test and restores the
// previous value. Tests set the package var directly rather than calling
// RegisterProviderRunner, which panics on a second registration by design.
func withRunner(t *testing.T, fn ProviderRunner) {
	t.Helper()
	prev := providerRunner
	providerRunner = fn
	t.Cleanup(func() { providerRunner = prev })
}

// newWorkspace returns a workspace rooted at a temp dir with dirs created, and the
// magusfile-discovered projects pre-registered as discovery would have left them.
// The root is symlink-resolved because Discover resolves it, and the provider path
// guard compares resolved paths against it.
func newWorkspace(t *testing.T, dirs []string, discovered ...string) *types.Workspace {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	for _, d := range dirs {
		require.NoError(t, os.MkdirAll(filepath.Join(root, filepath.FromSlash(d)), 0o755))
	}
	ws := &types.Workspace{Root: root, Projects: map[string]*types.Project{}}
	for _, p := range discovered {
		ws.Projects[p] = &types.Project{
			Path:   p,
			Dir:    filepath.Join(root, filepath.FromSlash(p)),
			Origin: types.OriginMagusfile,
		}
	}
	return ws
}

// addProvided runs the fold with caching off, which is what every test here wants
// except the cache tests: an empty cacheDir means "always ask the provider".
func addProvided(ctx context.Context, ws *types.Workspace, spellNames ...string) error {
	return AddProvidedProjects(ctx, ws, spellNames, ProviderCache{})
}

func TestAddProvidedProjectsNoProvidersIsNoOp(t *testing.T) {
	ws := newWorkspace(t, nil)
	withRunner(t, func(context.Context, string, string) ([]spells.ProvidedProject, error) {
		t.Fatal("runner must not be called when no provider is wired")
		return nil, nil
	})
	require.NoError(t, addProvided(context.Background(), ws))
	assert.Empty(t, ws.Projects)
}

func TestAddProvidedProjectsCarriesProvenance(t *testing.T) {
	ws := newWorkspace(t, []string{"libs/foo"})
	withRunner(t, func(_ context.Context, spellName, root string) ([]spells.ProvidedProject, error) {
		assert.Equal(t, "nx", spellName)
		assert.Equal(t, ws.Root, root)
		// Sources and Outputs are PROJECT-relative, the anchor baseStep joins against
		// the project path. A workspace-relative glob here would silently match nothing.
		return []spells.ProvidedProject{{
			Path:      "libs/foo",
			Name:      "@acme/foo",
			DependsOn: []string{"libs/shared"},
			Sources:   []string{"**/*.ts"},
			Outputs:   []string{"dist/**"},
		}}, nil
	})

	require.NoError(t, addProvided(context.Background(), ws, "nx"))

	got := ws.Projects["libs/foo"]
	require.NotNil(t, got, "provided project must be in the workspace")
	assert.Equal(t, &types.Project{
		Path:      "libs/foo",
		Name:      "@acme/foo",
		Origin:    types.ProvidedBy("nx"),
		Dir:       filepath.Join(ws.Root, "libs", "foo"),
		DependsOn: []string{"libs/shared"},
		Sources:   []string{"**/*.ts"},
		Outputs:   []string{"dist/**"},
	}, got)

	provider, ok := got.Origin.Provider()
	require.True(t, ok, "a provided project's origin must read back as one")
	assert.Equal(t, "nx", provider)
}

func TestAddProvidedProjectsMagusfileWins(t *testing.T) {
	ws := newWorkspace(t, []string{"libs/foo"}, "libs/foo")
	withRunner(t, func(context.Context, string, string) ([]spells.ProvidedProject, error) {
		return []spells.ProvidedProject{{Path: "libs/foo", Name: "from-provider"}}, nil
	})

	require.NoError(t, addProvided(context.Background(), ws, "nx"))

	got := ws.Projects["libs/foo"]
	require.NotNil(t, got)
	assert.Equal(t, types.OriginMagusfile, got.Origin, "a magusfile project keeps its provenance")
	assert.Empty(t, got.Name, "provider options must not reach a magusfile-declared project")
}

func TestAddProvidedProjectsFirstProviderOwnsThePath(t *testing.T) {
	ws := newWorkspace(t, []string{"libs/foo"})
	withRunner(t, func(_ context.Context, spellName, _ string) ([]spells.ProvidedProject, error) {
		return []spells.ProvidedProject{{Path: "libs/foo", Name: spellName}}, nil
	})

	require.NoError(t, addProvided(context.Background(), ws, "nx", "cargo"))

	require.Len(t, ws.Projects, 1)
	assert.Equal(t, types.ProvidedBy("nx"), ws.Projects["libs/foo"].Origin, "wiring order breaks the tie")
	assert.Equal(t, "nx", ws.Projects["libs/foo"].Name)
}

func TestAddProvidedProjectsOneProviderRepeatingAPath(t *testing.T) {
	ws := newWorkspace(t, []string{"libs/foo"})
	withRunner(t, func(context.Context, string, string) ([]spells.ProvidedProject, error) {
		return []spells.ProvidedProject{
			{Path: "libs/foo", Name: "first"},
			{Path: "libs/foo", Name: "second"},
		}, nil
	})

	require.NoError(t, addProvided(context.Background(), ws, "nx"),
		"a provider repeating one path is a duplicate record, not a failure")
	require.Len(t, ws.Projects, 1)
	assert.Equal(t, "first", ws.Projects["libs/foo"].Name)
}

func TestAddProvidedProjectsRejectsBadPaths(t *testing.T) {
	for _, tc := range []struct {
		name string
		path string
		want string
	}{
		{"empty", "", "a project path is required"},
		{"blank", "   ", "a project path is required"},
		{"root", ".", "belongs to the magusfile"},
		{"absolute", "/etc", "never absolute"},
		{"escaping", "../outside", "escapes the workspace root"},
		{"escaping after clean", "libs/../../outside", "escapes the workspace root"},
		{"missing", "libs/nope", "no such directory"},
		{"file", "libs/foo/file.txt", "a project is a directory"},
		{"symlink out of the tree", "libs/escape", "does not follow symlinked directories"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ws := newWorkspace(t, []string{"libs/foo"})
			require.NoError(t, os.WriteFile(filepath.Join(ws.Root, "libs", "foo", "file.txt"), []byte("x"), 0o644))
			outside := t.TempDir()
			require.NoError(t, os.Symlink(outside, filepath.Join(ws.Root, "libs", "escape")))
			withRunner(t, func(context.Context, string, string) ([]spells.ProvidedProject, error) {
				return []spells.ProvidedProject{{Path: "libs/foo"}, {Path: tc.path}}, nil
			})

			err := addProvided(context.Background(), ws, "nx")

			require.Error(t, err, "a path magus cannot accept must fail the load, not vanish")
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), string(types.ProviderPathRejected))
			// The record before the bad one DID land: the fold mutates in place and
			// returns at the first rejection, so a caller that ignores the error holds a
			// partial workspace. Every caller today discards it; this pins the shape so a
			// future one is not surprised by it.
			assert.Len(t, ws.Projects, 1)
			assert.NotNil(t, ws.Projects["libs/foo"])
		})
	}
}

// TestAddProvidedProjectsRejectionIsATypedDiagnostic keeps MGS1018 machine-readable:
// a consumer classifying by code must not have to match on the message text.
func TestAddProvidedProjectsRejectionIsATypedDiagnostic(t *testing.T) {
	ws := newWorkspace(t, nil)
	withRunner(t, func(context.Context, string, string) ([]spells.ProvidedProject, error) {
		return []spells.ProvidedProject{{Path: "../outside"}}, nil
	})

	err := addProvided(context.Background(), ws, "nx")

	// The code is itself the errors.Is sentinel; matching on it is what a consumer
	// classifying diagnostics does instead of matching the message.
	require.ErrorIs(t, err, types.ProviderPathRejected)
	assert.ErrorIs(t, err, types.ErrDiag)
}

func TestAddProvidedProjectsPropagatesRunnerError(t *testing.T) {
	ws := newWorkspace(t, nil)
	withRunner(t, func(context.Context, string, string) ([]spells.ProvidedProject, error) {
		return nil, assert.AnError
	})

	err := addProvided(context.Background(), ws, "nx")

	require.ErrorIs(t, err, assert.AnError)
	assert.Contains(t, err.Error(), `workspace provider "nx"`)
}

func TestAddProvidedProjectsEmptyResultIsNotFatal(t *testing.T) {
	ws := newWorkspace(t, nil)
	withRunner(t, func(context.Context, string, string) ([]spells.ProvidedProject, error) {
		return nil, nil
	})

	require.NoError(t, addProvided(context.Background(), ws, "nx"),
		"reporting nothing is warned about, not fatal: the tool may be mid-install, and failing here would brick every command including doctor")
	assert.Empty(t, ws.Projects)
}

func TestAddProvidedProjectsWithoutRunnerErrors(t *testing.T) {
	ws := newWorkspace(t, nil)
	withRunner(t, nil)

	err := addProvided(context.Background(), ws, "nx")

	require.Error(t, err, "a wired provider in a binary with no VM must say so, not silently drop every project")
	assert.Contains(t, err.Error(), "no runner registered")
}

func TestAddProvidedProjectsOptionErrorNamesTheProvider(t *testing.T) {
	ws := newWorkspace(t, []string{"libs/foo"})
	withRunner(t, func(context.Context, string, string) ([]spells.ProvidedProject, error) {
		// An absolute dependency path is rejected by WithDependsOn's own resolver, and
		// an unregistered spell by WithRegisteredSpell's - the provider's answer walks
		// the same validation a hand-authored project does.
		return []spells.ProvidedProject{{Path: "libs/foo", DependsOn: []string{"/absolute"}}}, nil
	})

	err := addProvided(context.Background(), ws, "nx")

	require.Error(t, err)
	assert.Contains(t, err.Error(), `workspace provider "nx"`)
	assert.Contains(t, err.Error(), "libs/foo")
}

func TestProjectOriginProvider(t *testing.T) {
	name, ok := types.ProvidedBy("nx").Provider()
	assert.True(t, ok)
	assert.Equal(t, "nx", name)

	_, ok = types.OriginMagusfile.Provider()
	assert.False(t, ok, "a magusfile origin has no provider")

	_, ok = types.ProjectOrigin("").Provider()
	assert.False(t, ok, "the zero origin has no provider")
}
