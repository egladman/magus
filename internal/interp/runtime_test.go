package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

type rootWorkspace struct {
	types.WorkspaceRepository
	root string
}

func (w rootWorkspace) Root() string { return w.root }

// An overlay entry is the more specific answer, so it outranks the reader; a path neither
// covers still comes from the disk only when no reader was supplied.
func TestReadSourcePrecedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte("disk"), 0o644))

	got, err := readSource(context.Background(), path)
	require.NoError(t, err)
	assert.Equal(t, "disk", string(got))

	read := func(string) ([]byte, error) { return []byte("revision"), nil }
	ctx := WithSourceReader(context.Background(), read)
	got, err = readSource(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, "revision", string(got))

	got, err = readSource(WithOverlay(ctx, map[string]string{path: "overlay"}), path)
	require.NoError(t, err)
	assert.Equal(t, "overlay", string(got))
}

// The id must be the one git itself assigns, or the trail's blob ids name nothing a
// reader can `git cat-file`.
func TestGitBlobIDMatchesGit(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "ce013625030ba8dba906f756967f9e9ca394464a", GitBlobID([]byte("hello\n")))
	assert.Equal(t, "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391", GitBlobID(nil))
}

// A load's log records what the reader actually returned, so a revision's bytes are
// logged under the revision's id rather than the disk's.
func TestSourceLogRecordsWhatTheLoadRead(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "magusfile.buzz")
	require.NoError(t, os.WriteFile(path, []byte("hello\n"), 0o644))

	var log SourceLog
	_, err := readSource(WithSourceLog(context.Background(), &log), path)
	require.NoError(t, err)
	assert.Equal(t, []SourceFile{{Path: path, BlobID: "ce013625030ba8dba906f756967f9e9ca394464a"}}, log.Files())

	var revised SourceLog
	ctx := WithSourceLog(WithSourceReader(context.Background(), func(string) ([]byte, error) { return nil, nil }), &revised)
	_, err = readSource(ctx, path)
	require.NoError(t, err)
	assert.Equal(t, []SourceFile{{Path: path, BlobID: "e69de29bb2d1d6434b8b29ae775ad8c2e48c5391"}}, revised.Files())
}

func TestMagusSearchPaths(t *testing.T) {
	t.Parallel()
	j := filepath.Join
	root := j("/", "w")
	project := j(root, "api")
	templatesUnder := func(dir string) []string {
		return []string{
			j(dir, "?.buzz"),
			j(dir, "?", "main.buzz"),
			j(dir, "?", "src", "main.buzz"),
			j(dir, "?", "src", "?.buzz"),
			j(dir, "magusfiles", "?.buzz"),
		}
	}

	t.Run("project then workspace root", func(t *testing.T) {
		ctx := types.WithWorkspace(context.Background(), rootWorkspace{root: root})
		want := append(templatesUnder(project), templatesUnder(root)...)
		assert.Equal(t, want, magusSearchPaths(ctx, project))
	})
	t.Run("project at the root is searched once", func(t *testing.T) {
		ctx := types.WithWorkspace(context.Background(), rootWorkspace{root: root})
		assert.Equal(t, templatesUnder(root), magusSearchPaths(ctx, root))
	})
	t.Run("no workspace searches only the project", func(t *testing.T) {
		assert.Equal(t, templatesUnder(project), magusSearchPaths(context.Background(), project))
	})
}

// A magusfile run via --root from another checkout must load its own modules,
// never the caller's.
func TestMagusfileImportIgnoresCwd(t *testing.T) {
	const magusfile = "import \"helper\";\nhelper\\touch();\n"
	const helper = "export fun touch() > void {}\n"

	cases := []struct {
		name          string
		projectHelper string
		cwdHelper     string
		wantErr       string
	}{
		{
			name:          "project module wins over a broken cwd module",
			projectHelper: helper,
			cwdHelper:     "this is not buzz\n",
		},
		{
			name:      "cwd module is not a fallback",
			cwdHelper: helper,
			wantErr:   `buzz: import "helper": module not found`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			project, cwd := t.TempDir(), t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(project, "magusfile.buzz"), []byte(magusfile), 0o644))
			if tc.projectHelper != "" {
				require.NoError(t, os.WriteFile(filepath.Join(project, "helper.buzz"), []byte(tc.projectHelper), 0o644))
			}
			require.NoError(t, os.WriteFile(filepath.Join(cwd, "helper.buzz"), []byte(tc.cwdHelper), 0o644))
			t.Chdir(cwd)

			src, err := Find(project)
			require.NoError(t, err)
			load, err := execBuzzSrc(context.Background(), src, true)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			t.Cleanup(func() { _ = load.Session.Close() })
		})
	}
}

// A remote spell is pulled and verified before Exec, so a bad pin stops the load with
// its code rather than surfacing later as an unbound name. The pull itself is
// internal/spell/remote's to test; MAGUS_OFFLINE keeps this one off the network.
func TestCheckRemoteSpellImports(t *testing.T) {
	t.Setenv("MAGUS_OFFLINE", "1")
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	pinned := "oci://ghcr.io/team/spells/x@" + digest.FromBytes([]byte("m")).String()

	assert.NoError(t, checkRemoteSpellImports(t.Context(), `import "spells/local" as local;`))

	err := checkRemoteSpellImports(t.Context(), `import "oci://ghcr.io/team/spells/x:latest" as x;`)
	require.ErrorIs(t, err, types.RemoteSpellUnpinned)

	err = checkRemoteSpellImports(t.Context(), `import "`+pinned+`";`)
	require.ErrorContains(t, err, "a remote spell must be aliased")

	err = checkRemoteSpellImports(t.Context(), `import "`+pinned+`" as x;`)
	require.ErrorContains(t, err, "is not cached and MAGUS_OFFLINE is set", "the import reaches the resolver")
}
