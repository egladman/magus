package job

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gitRepo is a git checkout at a temp dir holding files, with the box's own git config shut
// out so a global attributes file cannot name a driver the test did not.
func gitRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for name, body := range files {
		full := filepath.Join(dir, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	return dir
}

func TestRefuseUngradableClaims(t *testing.T) {
	t.Parallel()

	root := gitRepo(t, map[string]string{".gitattributes": "*.go diff=golang\n*.md diff=markdown\n", "run.go": "package run\n", "notes/a#b.md": "# A\n"})
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"no claim below a file reads nothing", []string{"run.go", "notes.txt"}, ""},
		{"a declaration of a driven file", []string{"run.go#executeStages", "docs/new.md#Usage"}, ""},
		{"a file with no driver", []string{"notes.txt#Intro"}, "\"notes.txt\" has no diff driver, so no changed line of it can be placed in a declaration; give `*.txt` one in .gitattributes"},
		{"an empty declaration", []string{"run.go#"}, `"run.go#" names no declaration after its #`},
		{"a pattern", []string{"*.go#executeStages"}, `"*.go#executeStages" claims a declaration of a pattern`},
		{"every bad entry at once", []string{"run.go#", "Makefile#all"}, `"run.go#" names no declaration after its #`},
		{"a path holding # spelled as a path", []string{"./notes/a#b.md", `notes/a\#b.md`, `notes/a\#b.md#A`}, ""},
		{"a path holding # left ambiguous", []string{"notes/a#b.md"},
			"\"notes/a#b.md\" is a path in this tree and also reads as a claim on \"notes/a\"; spell the path `./notes/a#b.md` or `notes/a\\#b.md`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := NewStore(tmpLoc(t, root))
			_, err := ForkMerge(context.Background(), s, "wave/job", func(u *types.Job) {
				u.State, u.WritePaths = types.StateDeclared, tc.paths
			}, config.Jobs{}, nil)
			if tc.want == "" {
				require.NoError(t, err)
				return
			}
			require.ErrorIs(t, err, types.WritePathClaimUngradable)
			assert.Contains(t, err.Error(), tc.want)
			rows, lerr := s.List()
			require.NoError(t, lerr)
			assert.Empty(t, rows, "a refused fork writes no row")
		})
	}

	t.Run("a checkout with no version control", func(t *testing.T) {
		t.Parallel()
		s := NewStore(tmpLoc(t, t.TempDir()))
		err := RefuseUngradableClaims(context.Background(), s, "wave/job", types.Job{WritePaths: []string{"run.go#executeStages"}})
		require.ErrorIs(t, err, types.WritePathClaimUngradable)
		assert.Contains(t, err.Error(), `"run.go": `)
	})
}
