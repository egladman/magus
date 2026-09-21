package interp

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMagusSearchPathsHaveNoCwdRoot(t *testing.T) {
	t.Parallel()
	project := t.TempDir()
	for _, p := range magusSearchPaths(context.Background(), project) {
		assert.True(t, filepath.IsAbs(p), "search path %q resolves against the process cwd", p)
	}
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
		wantErr       bool
	}{
		{
			name:          "project module wins over a broken cwd module",
			projectHelper: helper,
			cwdHelper:     "this is not buzz\n",
		},
		{
			name:      "cwd module is not a fallback",
			cwdHelper: helper,
			wantErr:   true,
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
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			_ = load.Session.Close()
		})
	}
}
