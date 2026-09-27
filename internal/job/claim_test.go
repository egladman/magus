package job

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
				u.Check, u.State, u.WritePaths = forkCheck(), types.StateDeclared, tc.paths
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

// commitRepo commits everything gitRepo wrote and returns HEAD, so a footprint has a
// revision to diff against.
func commitRepo(t *testing.T, dir string) string {
	t.Helper()
	run := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com")
		out, err := cmd.CombinedOutput()
		require.NoError(t, err, "%s", out)
		return strings.TrimSpace(string(out))
	}
	run("add", "-A")
	run("commit", "-q", "-m", "seed")
	return run("rev-parse", "HEAD")
}

func TestRefuseUnorderedFileShare(t *testing.T) {
	t.Parallel()

	files := map[string]string{"magusfile.buzz": "target a {}\n", "notes.txt": "hi\n", "docs/x.md": "# X\n"}
	bRoot := gitRepo(t, files)
	aRoot := gitRepo(t, files)
	aRev := commitRepo(t, aRoot)

	for _, tc := range []struct {
		name        string
		aWrite      []string
		aCheckout   *string // nil: aRoot (a different checkout than the store's); &"": untaken
		aDependsOn  []string
		bWrite      []string
		bParent     string
		bDependsOn  []string
		wantRefused bool
	}{
		{name: "unordered whole-file share", aWrite: []string{"magusfile.buzz"}, bWrite: []string{"magusfile.buzz"}, wantRefused: true},
		{name: "different declarations of one file pass", aWrite: []string{"magusfile.buzz#lint_files"}, bWrite: []string{"magusfile.buzz#lint"}},
		{name: "depends_on orders the pair", aWrite: []string{"magusfile.buzz"}, bWrite: []string{"magusfile.buzz"}, bDependsOn: []string{"A"}},
		{name: "a child of the holder", aWrite: []string{"magusfile.buzz"}, bWrite: []string{"magusfile.buzz"}, bParent: "A"},
		{name: "a glob is not a literal file", aWrite: []string{"docs/x.md"}, bWrite: []string{"docs/**"}},
		{name: "a file with no diff driver", aWrite: []string{"notes.txt"}, bWrite: []string{"notes.txt"}},
		{name: "an untaken holder is left alone", aWrite: []string{"magusfile.buzz"}, aCheckout: strPtr(""), bWrite: []string{"magusfile.buzz"}},
		{name: "a holder in the same checkout is left alone", aWrite: []string{"magusfile.buzz"}, aCheckout: strPtr(bRoot), bWrite: []string{"magusfile.buzz"}},
		{name: "a holder blocked on depends_on is left alone", aWrite: []string{"magusfile.buzz"}, aDependsOn: []string{"C"}, bWrite: []string{"magusfile.buzz"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			loc := tmpLoc(t, bRoot)
			s := NewStore(loc)
			checkout := aRoot
			if tc.aCheckout != nil {
				checkout = *tc.aCheckout
			}
			a := types.Job{ID: "A", State: types.StateRunning, WritePaths: tc.aWrite, DependsOn: tc.aDependsOn, Checkpoint: aRev, CheckoutRoot: checkout}
			seed(t, s, a)
			rows, err := s.List()
			require.NoError(t, err)

			candidate := types.Job{ID: "B", WritePaths: tc.bWrite, Parent: tc.bParent, DependsOn: tc.bDependsOn}
			refErr := RefuseUnorderedFileShare(context.Background(), s, rows, "B", candidate)
			if !tc.wantRefused {
				require.NoError(t, refErr)
				return
			}
			require.ErrorIs(t, refErr, types.WritePathFileShared)
			assert.Contains(t, refErr.Error(), `job: B declares "magusfile.buzz", and A`)
			assert.Contains(t, refErr.Error(), "none yet")
			assert.Contains(t, refErr.Error(), "Claim `magusfile.buzz#<declaration>`")
			assert.Contains(t, refErr.Error(), "--depends-on A")
		})
	}
}

// A holder that exited is done editing, so the file its row still lists is no longer one
// a fork in another checkout shares with it (MGS3032); a running holder still refuses.
func TestExitedHolderClaimsNoFileShare(t *testing.T) {
	t.Parallel()

	files := map[string]string{"magusfile.buzz": "target a {}\n"}
	bRoot := gitRepo(t, files)
	aRoot := gitRepo(t, files)
	aRev := commitRepo(t, aRoot)

	for state, refused := range map[types.JobState]bool{types.StateRunning: true, types.StateExited: false} {
		s := NewStore(tmpLoc(t, bRoot))
		seed(t, s, types.Job{ID: "A", State: state, WritePaths: []string{"magusfile.buzz"}, Checkpoint: aRev, CheckoutRoot: aRoot})
		rows, err := s.List()
		require.NoError(t, err)

		err = RefuseUnorderedFileShare(context.Background(), s, rows, "B", types.Job{ID: "B", WritePaths: []string{"magusfile.buzz"}})
		if refused {
			require.ErrorIs(t, err, types.WritePathFileShared, state)
			continue
		}
		require.NoError(t, err, state)
	}
}

// strPtr returns a pointer to s, for a test table field that must tell "not given" (nil)
// apart from "given as empty".
func strPtr(s string) *string { return &s }

// TestRefuseUnorderedFileShareTransitiveDependsOn pins that depends_on orders a pair even
// through an intermediate row (C3): B depends on M, M depends on A, so B and A are ordered
// though neither names the other directly.
func TestRefuseUnorderedFileShareTransitiveDependsOn(t *testing.T) {
	t.Parallel()

	files := map[string]string{"magusfile.buzz": "target a {}\n"}
	bRoot := gitRepo(t, files)
	aRoot := gitRepo(t, files)
	aRev := commitRepo(t, aRoot)

	loc := tmpLoc(t, bRoot)
	s := NewStore(loc)
	seed(t, s, types.Job{ID: "A", State: types.StateRunning, WritePaths: []string{"magusfile.buzz"}, Checkpoint: aRev, CheckoutRoot: aRoot})
	seed(t, s, types.Job{ID: "M", State: types.StateRunning, WritePaths: []string{"magusfile.buzz"}, Checkpoint: aRev, CheckoutRoot: aRoot, DependsOn: []string{"A"}})
	rows, err := s.List()
	require.NoError(t, err)

	candidate := types.Job{ID: "B", WritePaths: []string{"magusfile.buzz"}, DependsOn: []string{"M"}}
	require.NoError(t, RefuseUnorderedFileShare(context.Background(), s, rows, "B", candidate))
}
