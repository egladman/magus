package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/vcs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStatusPaths pins the porcelain parse. DirtyFiles returns status LINES despite its
// name, and every other caller only checks the result for emptiness - so an unparsed line
// reaches the classifier as " M foo", matches no glob, and reports the whole workspace as
// undeclared (or with --untracked, stages it).
func TestStatusPaths(t *testing.T) {
	tests := []struct {
		name  string
		lines []string
		want  []string
	}{
		{"modified", []string{" M cmd/magus/agent.go"}, []string{"cmd/magus/agent.go"}},
		{"staged add", []string{"A  docs/new.md"}, []string{"docs/new.md"}},
		{"untracked", []string{"?? scratch.txt"}, []string{"scratch.txt"}},
		{"both columns", []string{"MM internal/agent/catalog.go"}, []string{"internal/agent/catalog.go"}},
		// A rename must stage the NEW name; the old one no longer exists.
		{"rename", []string{"R  old/path.go -> new/path.go"}, []string{"new/path.go"}},
		// git quotes a path containing whitespace or unusual bytes.
		{"quoted path", []string{` M "docs/a file.md"`}, []string{"docs/a file.md"}},
		{"several", []string{" M a.go", "?? b.go"}, []string{"a.go", "b.go"}},

		// DirtyFiles reads status output through a trimming helper, so the FIRST line
		// loses its leading space. Unhandled, the path keeps an "M " on the front and
		// reports as undeclared - how .gitattributes showed up as residue in a real run.
		{"first line trimmed of its leading column", []string{"M .gitattributes"}, []string{".gitattributes"}},
		{"trimmed first line then a normal one", []string{"M a.go", " M b.go"}, []string{"a.go", "b.go"}},
		// hg reports one status column, not two.
		{"hg single column", []string{"M docs/foo.md"}, []string{"docs/foo.md"}},
		{"clean tree", nil, []string{}},

		// jj's DirtyFiles runs `jj diff --name-only` and returns BARE paths. Slicing two
		// columns off those corrupts every one, so a jj workspace stages nothing and
		// reports the tree as undeclared rather than misparsed.
		{"bare path is left intact", []string{"docs/foo.md"}, []string{"docs/foo.md"}},
		{"short bare path is not dropped", []string{"a.go"}, []string{"a.go"}},
		{"bare path with a space", []string{"my file.md"}, []string{"my file.md"}},

		// Only git's quoting form may be unquoted: Go's Unquote also takes raw strings
		// and rune literals, rewriting legitimate filenames.
		{"backquoted name is not unquoted", []string{" M " + "`x`"}, []string{"`x`"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, statusPaths(tt.lines))
		})
	}
}

func TestStatusPrefix(t *testing.T) {
	assert.Equal(t, " M ", statusPrefix(" M a.go"))
	assert.Equal(t, "MM ", statusPrefix("MM a.go"))
	assert.Equal(t, "?? ", statusPrefix("?? a.go"))
	assert.Equal(t, "M ", statusPrefix("M .gitattributes"), "a trimmed git line, or hg, has one column")
	assert.Equal(t, "", statusPrefix("docs/foo.md"), "a bare path has no status columns")
	assert.Equal(t, "", statusPrefix("a.go"))
	assert.Equal(t, "", statusPrefix("ab"), "too short to carry a prefix")
}

// TestSplitVCSVerb pins that the subcommand is found past a leading flag. Reading args[0]
// alone let `magus vcs -q merge-driver ...` miss the merge-driver dispatch profile, which
// keeps a workspace load - and its .gitattributes write - out of git's index
// manipulation.
func TestSplitVCSVerb(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		wantVerb string
		wantRest []string
	}{
		{"plain", []string{"resolve"}, "resolve", nil},
		{"verb then args", []string{"add", "a.go"}, "add", []string{"a.go"}},
		{"flag before verb", []string{"-q", "merge-driver", "%O"}, "merge-driver", []string{"-q", "%O"}},
		{"long flag before verb", []string{"--silent", "add"}, "add", []string{"--silent"}},
		{"help flag is its own verb", []string{"-h"}, "-h", nil},
		{"help flag after a flag", []string{"-q", "--help"}, "--help", []string{"-q"}},
		{"no verb at all", []string{"-q"}, "", []string{"-q"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			verb, rest := splitVCSVerb(tt.args)
			assert.Equal(t, tt.wantVerb, verb)
			if tt.wantRest == nil {
				assert.Empty(t, rest)
				return
			}
			assert.Equal(t, tt.wantRest, rest)
		})
	}
}

func TestWorkspaceRelPaths(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "cmd", "magus")
	require.NoError(t, os.MkdirAll(sub, 0o755))

	t.Chdir(sub)

	got, err := workspaceRelPaths(root, []string{"vcs.go", "../../README.md"})
	require.NoError(t, err)
	assert.Equal(t, []string{"cmd/magus/vcs.go", "README.md"}, got,
		"paths a user types are relative to their cwd, not the workspace root")

	got, err = workspaceRelPaths(root, []string{filepath.Join(root, "a.go")})
	require.NoError(t, err)
	assert.Equal(t, []string{"a.go"}, got, "an absolute path is made workspace-relative")

	_, err = workspaceRelPaths(root, []string{"../../../outside.go"})
	require.Error(t, err, "a path escaping the workspace is refused rather than silently addressed")

	got, err = workspaceRelPaths(root, nil)
	require.NoError(t, err)
	assert.Nil(t, got)
}

// TestClassifyForStaging pins the grouping, and the doctrine behind it: a generated
// output belongs in the SAME commit as the source that moved it, so both are staged;
// an undeclared path is the `git add -A` hazard, so it is reported instead.
func TestClassifyForStaging(t *testing.T) {
	out := []types.FileEntry{
		{Path: "cmd/magus/agent.go", Role: "source"},
		{Path: "MAGUS.md", Role: "output"},
		{Path: "docs/gen/index.html", Role: "output"},
		{Path: "scratch.txt", Role: "unclaimed"},
		{Path: "notes.md", Role: ""},
	}

	sources, outputs, undeclared := classifyForStaging(out)
	assert.Equal(t, []string{"cmd/magus/agent.go"}, sources)
	assert.Equal(t, []string{"MAGUS.md", "docs/gen/index.html"}, outputs)
	assert.Equal(t, []string{"scratch.txt", "notes.md"}, undeclared,
		"anything not declared source or output is undeclared, including an empty role")
}

func TestSplitMaintained(t *testing.T) {
	maintained, unclaimed := splitMaintained([]string{".gitattributes", "scratch.txt"})
	assert.Equal(t, []string{".gitattributes"}, maintained,
		"a file magus's own core writes is not residue, and must not be reported as such")
	assert.Equal(t, []string{"scratch.txt"}, unclaimed)
}

// TestFilterStageable pins the fix for a data-loss bug. `vcs add` hands a project's
// declared outputs to one `git add`; if one path no longer exists on disk (a renamed
// directory with a stale declaration), git fails that pathspec with "did not match any
// files" and aborts the WHOLE invocation, staging nothing. You think you staged and did
// not.
//
// So: keep every path that exists; keep a path gone from disk but still tracked, since
// that is how a deletion or rename gets recorded; drop only what is neither.
func TestFilterStageable(t *testing.T) {
	dir := initGitRepo(t)

	// tracked.txt is committed, then deleted from disk without `git rm` - the "old half
	// of a rename" case: gone from disk, still tracked.
	trackedPath := filepath.Join(dir, "tracked.txt")
	require.NoError(t, os.WriteFile(trackedPath, []byte("x"), 0o644))
	runGit(t, dir, "add", "tracked.txt")
	runGit(t, dir, "commit", "-m", "seed")
	require.NoError(t, os.Remove(trackedPath))

	// present.txt exists on disk and was never committed - the ordinary new-file case.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "present.txt"), []byte("y"), 0o644))

	// stale.txt is a declared output that never existed on disk and was never tracked -
	// e.g. libs/diag/MAGUS.md after libs/diag was renamed to libs/diagnostics. This is
	// the path that must be dropped, not fed to `git add`.
	paths := []string{"tracked.txt", "present.txt", "stale.txt"}

	stageable, dropped, err := filterStageable(context.Background(), dir, "git", paths)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"tracked.txt", "present.txt"}, stageable)
	assert.Equal(t, []string{"stale.txt"}, dropped)
}

// TestFilterStageableNonGit pins that the probe is skipped for backends without git's
// abort-the-whole-invocation behavior, rather than shelling out to a missing command.
func TestFilterStageableNonGit(t *testing.T) {
	dir := t.TempDir()
	stageable, dropped, err := filterStageable(context.Background(), dir, "hg", []string{"gone.txt"})
	require.NoError(t, err)
	assert.Equal(t, []string{"gone.txt"}, stageable)
	assert.Empty(t, dropped)
}

// TestEmittedSelectionSurvivesStalePath proves the end-to-end property the filter
// exists for: the selection `vcs add -o name` emits must be safe to feed to git in
// ONE call, even when a declared path no longer corresponds to anything real. The
// field failure this pins, from the era when vcs add staged directly:
//
//	staged 3 undeclared file(s) (--untracked):
//	  .gitattributes
//	  libs/diag/go.mod
//	  libs/diagnostics/
//	fatal: pathspec 'libs/diag/MAGUS.md' did not match any files
//	[error] vcs add: git add: exit status 128
//
// where every one of the first three paths was silently left unstaged. The emitter
// keeps the guarantee by dropping the stale path BEFORE it reaches the pipe.
func TestEmittedSelectionSurvivesStalePath(t *testing.T) {
	dir := initGitRepo(t)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "present.txt"), []byte("y"), 0o644))

	selection, dropped, err := filterStageable(context.Background(), dir, "git", []string{"present.txt", "stale.txt"})
	require.NoError(t, err)
	assert.Equal(t, []string{"stale.txt"}, dropped)

	// The documented pipe, verbatim: the emitted selection through
	// `git add --pathspec-from-file=-`.
	cmd := exec.Command("git", "-C", dir, "add", "--pathspec-from-file=-")
	cmd.Stdin = strings.NewReader(strings.Join(selection, "\n") + "\n")
	out, err := cmd.CombinedOutput()
	require.NoErrorf(t, err, "git add --pathspec-from-file=-: %s", out)

	staged, err := exec.Command("git", "-C", dir, "diff", "--cached", "--name-only").Output()
	require.NoError(t, err)
	assert.Equal(t, "present.txt\n", string(staged),
		"present.txt must be staged even though stale.txt was in the classified set")
}

// resolveWorkspace opens a real root-project workspace whose generate target
// declares gen/** as its output - the shape settleTarget requires - mirroring
// mergeDriverWorkspace but returning the Magus itself for the decision layer.
func resolveWorkspace(t *testing.T) (*magus.Magus, string) {
	t.Helper()
	root := t.TempDir()
	magusfile := `import "magus";
import "fs";

magus.project({})

export fun generate(ctx: magus\Context, args: [str]) > void {
    ctx.writesFiles("gen/**");
    fs\writeFile("gen/regenerated.txt", "regenerated");
}
`
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(magusfile), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "gen"), 0o755))
	m, err := magus.Open(context.Background(), root)
	require.NoError(t, err, "fixture workspace must open")
	return m, root
}

// stubResolver satisfies types.ConflictResolver for planResolution, which only
// calls IgnoredPaths.
type stubResolver struct{ ignored map[string]bool }

func (s stubResolver) Conflicts(context.Context, string) ([]types.Conflict, error) { return nil, nil }
func (s stubResolver) KeepIncoming(context.Context, string, []string) error        { return nil }
func (s stubResolver) MarkResolved(context.Context, string, []string) error        { return nil }
func (s stubResolver) RemoveConflicts(context.Context, string, []string) error     { return nil }
func (s stubResolver) IgnoredPaths(context.Context, string, []string) (map[string]bool, error) {
	return s.ignored, nil
}

// TestPlanResolutionDecisions pins the whole decision table in one plan: every
// conflict shape lands in exactly one bucket, and the rebuild map is keyed by
// the string `magus run` accepts - the root project is ".", never the checkout
// directory's basename (a git worktree's basename names no known project, which
// is how resolve once regenerated nothing and died with `unknown project`).
func TestPlanResolutionDecisions(t *testing.T) {
	m, _ := resolveWorkspace(t)
	conflicts := []types.Conflict{
		{Path: "gen/kept.txt", Kind: types.ConflictKindContent},
		{Path: "gen/ignored.txt", Kind: types.ConflictKindDeleted},
		{Path: "gen/deleted.txt", Kind: types.ConflictKindDeleted},
		{Path: "gen/both.txt", Kind: types.ConflictKindBothDeleted},
		{Path: "main.go", Kind: types.ConflictKindContent},
		{Path: ".gitattributes", Kind: types.ConflictKindContent},
	}

	plan, err := planResolution(context.Background(), m,
		stubResolver{ignored: map[string]bool{"gen/ignored.txt": true}}, conflicts)
	require.NoError(t, err)

	assert.Equal(t, resolutionPlan{
		keep:     []string{"gen/kept.txt"},
		gone:     []string{"gen/both.txt", "gen/ignored.txt"},
		rederive: []string{".gitattributes"},
		manual:   []string{"gen/deleted.txt", "main.go"},
		rebuild:  map[string][]string{"generate": {"."}},
	}, plan,
		"content output -> keep; deleted+ignored and both-deleted -> gone; deleted-but-tracked and undeclared -> manual; the driver registration -> rederive")
}

// TestSettledPathsIncludesRootProjectOutputs pins the regression where the
// rebuilt-set lookup used the display label (the checkout dir basename for a
// root project) against keys built from the run arg ".": every OTHER
// regenerated root-project output was silently left unstaged, which is exactly
// the leftover dirty tree that makes `git rebase --continue` refuse.
func TestSettledPathsIncludesRootProjectOutputs(t *testing.T) {
	m, root := resolveWorkspace(t)
	runGit(t, root, "init")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "test")
	require.NoError(t, os.WriteFile(filepath.Join(root, "gen", "other.txt"), []byte("old"), 0o644))
	runGit(t, root, "add", "gen/other.txt")
	runGit(t, root, "commit", "-m", "seed")
	// The regeneration rewrote a declared output that was never itself conflicted.
	require.NoError(t, os.WriteFile(filepath.Join(root, "gen", "other.txt"), []byte("new"), 0o644))

	res, err := vcs.Resolve(context.Background(), root, "", types.VCSOptions{})
	require.NoError(t, err)
	plan := resolutionPlan{
		keep:    []string{"gen/kept.txt"},
		rebuild: map[string][]string{"generate": {"."}},
	}

	settled, err := settledPaths(context.Background(), m, res.VCS, plan)
	require.NoError(t, err)
	assert.Contains(t, settled, "gen/kept.txt", "the conflicted path itself is always recorded")
	assert.Contains(t, settled, "gen/other.txt",
		"a rebuilt root project's other dirty outputs must be recorded; the lookup key is the run arg \".\"")
}

// initGitRepo creates a temp git repo with an identity, so a commit can be made in it.
// Skip-on-missing-git follows TestParseBisectCulprit.
func initGitRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init").CombinedOutput(); err != nil {
		t.Skipf("git init failed: %v\n%s", err, out)
	}
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "test")
	return dir
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	require.NoErrorf(t, err, "git %v: %s", args, out)
}
