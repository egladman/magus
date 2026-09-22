package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type rootWorkspace struct {
	types.WorkspaceRepository
	root string
}

func (w rootWorkspace) Root() string { return w.root }

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
