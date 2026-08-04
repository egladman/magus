package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/egladman/magus/types"
)

// deadOutputRepo builds the shape that broke CI: a project declaring two generated trees as
// outputs, one COMMITTED (src/gen, written by buf-generate) and one untracked (gen/, written by
// the build). built controls whether the build has run.
func deadOutputRepo(t *testing.T, built bool) string {
	t.Helper()
	repo := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		require.NoError(t, cmd.Run(), "git %v", args)
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(repo, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}

	// Anchored, as the real one is (`console/gen/`). A bare `gen/` matches at any
	// depth and would swallow src/gen too, collapsing the two cases this fixture exists
	// to keep apart.
	write(".gitignore", "/gen/\n")
	// Nested exactly as console/src/gen is, so `src/gen/*` globs to a DIRECTORY. That is
	// the case the tracked lookup has to survive: ls-files echoes the files underneath a
	// directory argument, never the directory itself.
	write("src/gen/magus/activity/v1/activity_pb.ts", "// generated, committed\n")
	run("init", "-q", "-b", "main")
	run("add", "-A")
	run("commit", "-qm", "init")

	if built {
		write("gen/index.html", "<html></html>")
	}
	return repo
}

func deadOutputProject(dir string) *types.Project {
	return &types.Project{Path: "console", Name: "console", Dir: dir, Outputs: []string{"gen/**", "src/gen/**"}}
}

// TestDeadOutputGlobsIgnoresCommittedOutputs is the regression. On a fresh clone src/gen/**
// matches (it is committed) while gen/** does not (it is gitignored and unbuilt), and reading
// that as "the project was built, so gen/** is dead" failed `magus doctor` on every CI runner
// while passing on every developer machine, where gen/ had already been built.
func TestDeadOutputGlobsIgnoresCommittedOutputs(t *testing.T) {
	repo := deadOutputRepo(t, false)
	r := &runner{root: repo, ws: stubWorkspace{}}

	got := r.checkDeadOutputGlobs([]*types.Project{deadOutputProject(repo)})

	assert.Equal(t, Check{
		Name:    "dead output globs",
		Status:  StatusOK,
		Message: "no dead output globs",
	}, got, "a committed generated tree is not evidence the project was built")
}

// TestDeadOutputGlobsReportsOnceBuilt keeps the teeth. Once an UNTRACKED output exists the
// project really has been built, so a sibling glob still matching nothing is the genuine
// finding the check exists for: a target inheriting it fails its snapshot on a cold cache.
func TestDeadOutputGlobsReportsOnceBuilt(t *testing.T) {
	repo := deadOutputRepo(t, true)
	p := deadOutputProject(repo)
	p.Outputs = []string{"gen/**", "src/gen/**", "dist/**"}
	r := &runner{root: repo, ws: stubWorkspace{}}

	got := r.checkDeadOutputGlobs([]*types.Project{p})

	assert.Equal(t, StatusFail, got.Status)
	assert.Equal(t,
		[]string{`console: output glob "dist/**" matched no files while the project's other outputs did`},
		got.Details,
		"only dist/** is dead; gen/** is built and src/gen/** is committed")
}

// TestDeadOutputGlobsWithoutTrackedReporter pins the degrade path: with no VCS to ask, presence
// is the only signal there is, so the check keeps its pre-existing behavior rather than
// silently reporting nothing.
func TestDeadOutputGlobsWithoutTrackedReporter(t *testing.T) {
	dir := t.TempDir() // not a repository, so vcs.Resolve finds nothing
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "src", "gen"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "src", "gen", "a.ts"), []byte("x"), 0o644))
	r := &runner{root: dir, ws: stubWorkspace{}}

	got := r.checkDeadOutputGlobs([]*types.Project{deadOutputProject(dir)})

	assert.Equal(t, StatusFail, got.Status)
	assert.Equal(t,
		[]string{`console: output glob "gen/**" matched no files while the project's other outputs did`},
		got.Details)
}

// TestOutputOwnedByTwoTargets is MGS1020. The failing shape is a generator and a formatter
// both declaring a gen/ tree: no ordering resolves it, so it has to be reported rather than
// scheduled around.
func TestOutputOwnedByTwoTargets(t *testing.T) {
	var r *runner

	t.Run("two targets on one glob is reported", func(t *testing.T) {
		got := r.checkOutputOwnedByTwoTargets([]*types.Project{{
			Path: "docs", Name: "docs",
			TargetOutputs: map[string][]types.OutputRef{
				"generate": {{Glob: "gen/**"}},
				"format":   {{Glob: "gen/**"}},
			},
		}})
		assert.Equal(t, StatusFail, got.Status)
		assert.Equal(t,
			[]string{`docs: output glob "gen/**" is declared by format and generate`},
			got.Details, "owners are sorted so the message is stable")
	})

	t.Run("one owner per glob is fine", func(t *testing.T) {
		got := r.checkOutputOwnedByTwoTargets([]*types.Project{{
			Path: "docs", Name: "docs",
			TargetOutputs: map[string][]types.OutputRef{
				"generate":      {{Glob: "gen/**"}},
				"build-mermaid": {{Glob: "gen/assets/mermaid.js"}},
			},
		}})
		assert.Equal(t, Check{
			Name:    "output ownership",
			Status:  StatusOK,
			Message: "every declared output has one owning target",
		}, got, "distinct globs are not an overlap, even nested ones")
	})

	t.Run("one target repeating a glob is not two owners", func(t *testing.T) {
		got := r.checkOutputOwnedByTwoTargets([]*types.Project{{
			Path: "docs", Name: "docs",
			TargetOutputs: map[string][]types.OutputRef{
				"generate": {{Glob: "gen/**"}, {Glob: "gen/**"}},
			},
		}})
		assert.Equal(t, StatusOK, got.Status)
	})
}
