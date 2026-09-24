package doctor

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/config"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/internal/sessions"
	"github.com/egladman/magus/internal/trail"
	"github.com/egladman/magus/spells"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeJournal fabricates the run journal StalledTargets reads: one JSONL record per
// target result, with "pass" meaning it executed and "cached" meaning it replayed. root
// is the workspace root, so the journal lands under .magus/runs where runner.cacheDir
// looks for it.
func writeJournal(t *testing.T, root string, records []map[string]any) {
	t.Helper()
	dir := filepath.Join(root, ".magus", cache.RunsDir)
	require.NoError(t, os.MkdirAll(dir, 0o755))
	var b strings.Builder
	for _, rec := range records {
		line, err := json.Marshal(rec)
		require.NoError(t, err)
		b.Write(line)
		b.WriteByte('\n')
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, "run-1.jsonl"), []byte(b.String()), 0o644))
}

func ranNTimes(project, target string, n int) []map[string]any {
	out := make([]map[string]any, 0, n)
	for range n {
		out = append(out, map[string]any{
			"kind": "result", "project": project, "target": target,
			"status": "pass", "dur_ms": 3000, // above MinAvgMsForYield
		})
	}
	return out
}

// TestCacheYieldExemptsSkipCacheTargets is the MGS1009 fix. A drift gate declares
// skip_cache precisely so it always runs, so "0 cached" is the designed outcome and
// reporting it fails a workspace for being correct.
func TestCacheYieldExemptsSkipCacheTargets(t *testing.T) {
	dir := t.TempDir()
	writeJournal(t, dir, ranNTimes(".", "generate:rw", 12))

	projects := []*types.Project{{
		Path: ".",
		TargetPolicies: map[string]types.Target{
			"generate": {SkipCache: true, SkipCacheReason: "is the drift gate itself"},
		},
	}}
	r := &runner{root: dir, ws: stubWorkspace{}}

	got := r.checkCacheYield(projects)

	assert.Equal(t, types.Check{
		Name:    "cache-yield",
		Status:  types.CheckOK,
		Message: "no target is running uncached (1 declared skip_cache)",
	}, got, "the charm suffix must not defeat the policy lookup, and the exemption stays visible")
}

// TestCacheYieldStillReportsUndeclaredTargets keeps the teeth: a target that never
// replays and never claimed it would is the finding the check exists for.
func TestCacheYieldStillReportsUndeclaredTargets(t *testing.T) {
	dir := t.TempDir()
	records := append(ranNTimes(".", "generate:rw", 12), ranNTimes("docs", "build", 9)...)
	writeJournal(t, dir, records)

	projects := []*types.Project{{
		Path:           ".",
		TargetPolicies: map[string]types.Target{"generate": {SkipCache: true, SkipCacheReason: "drift gate"}},
	}, {Path: "docs"}}
	r := &runner{root: dir, ws: stubWorkspace{}}

	got := r.checkCacheYield(projects)

	require.Equal(t, types.CheckFail, got.Status)
	require.Len(t, got.Details, 2, "one finding plus the advice line")
	assert.Contains(t, got.Details[0], "docs build: 9 runs, 0 cached")
	assert.NotContains(t, strings.Join(got.Details, "\n"), "generate",
		"the skip_cache target is excluded, not merely sorted below")
}

// TestCacheYieldAdviceNamesBothCauses pins the wording. The old line asserted a wide
// footprint, which is wrong for a version-stamped binary: go-build embeds `git describe`
// and the commit hash, so every commit mints a new key and no footprint change fixes it.
func TestCacheYieldAdviceNamesBothCauses(t *testing.T) {
	dir := t.TempDir()
	writeJournal(t, dir, ranNTimes(".", "go-build:rw", 9))

	r := &runner{root: dir, ws: stubWorkspace{}}
	got := r.checkCacheYield(nil)

	require.Equal(t, types.CheckFail, got.Status)
	advice := got.Details[len(got.Details)-1]
	assert.Contains(t, advice, "wider than it reads", "the footprint cause")
	assert.Contains(t, advice, "volatile state", "the version-stamp cause")
}

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
	write("src/gen/magus/activity/v1alpha1/activity_pb.ts", "// generated, committed\n")
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

	assert.Equal(t, types.Check{
		Name:    "dead-output-globs",
		Status:  types.CheckOK,
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

	assert.Equal(t, types.CheckFail, got.Status)
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

	assert.Equal(t, types.CheckFail, got.Status)
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
		assert.Equal(t, types.CheckFail, got.Status)
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
		assert.Equal(t, types.Check{
			Name:    "output-ownership",
			Status:  types.CheckOK,
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
		assert.Equal(t, types.CheckOK, got.Status)
	})
}

// TestGlobOutputs_CrossesDirectories pins the reason this uses doublestar. filepath.Glob's
// `*` does not cross separators, so the previous `**` -> `*` rewrite silently matched
// nothing two levels deep and reported a live output as a dead glob.
func TestGlobOutputs_CrossesDirectories(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gen", "a", "b"), 0o755))
	deep := filepath.Join(dir, "gen", "a", "b", "c.js")
	require.NoError(t, os.WriteFile(deep, []byte("x"), 0o644))
	shallow := filepath.Join(dir, "gen", "top.js")
	require.NoError(t, os.WriteFile(shallow, []byte("x"), 0o644))

	hits, err := globOutputs(dir, "gen/**/*.js")
	require.NoError(t, err)
	assert.Contains(t, hits, deep, "a file two directories deep must match gen/**/*.js")

	hits, err = globOutputs(dir, "gen/*.js")
	require.NoError(t, err)
	assert.Contains(t, hits, shallow)
	assert.NotContains(t, hits, deep, "a single star must still not cross a separator")
}

// writeGuardProbeStub writes root/magus as a stub binary standing in for the
// real one: the probe only cares that `magus shell -o name` prints a decision
// on stdout and exits accordingly, never about actual guard rules.
func writeGuardProbeStub(t *testing.T, root, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte(body), 0o755))
}

const denyingProbeStub = "#!/bin/sh\necho deny\nexit 2\n"

// testProbeBudget is far above the shipped guardProbeBudget on purpose.
// These subtests exec a real child process, and this repo is developed across
// many concurrent worktrees: a machine busy enough to push a trivial exec
// past the production budget would make this suite flaky for a reason that
// says nothing about the check. The production value stays 5s; only the test
// waits longer.
const testProbeBudget = 60 * time.Second

func TestCheckGuardWiring(t *testing.T) {
	t.Run("no binary resolves at all -> fail", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("PATH", t.TempDir()) // empty: no magus anywhere
		c := checkGuardWiring(context.Background(), root, testProbeBudget)
		require.Equal(t, types.CheckFail, c.Status)
		assert.Contains(t, c.Message, "no ./magus and no magus on PATH")
	})

	t.Run("probe does not return a deny -> fail with observed output", func(t *testing.T) {
		root := t.TempDir()
		writeGuardProbeStub(t, root, "#!/bin/sh\nexit 0\n")

		c := checkGuardWiring(context.Background(), root, testProbeBudget)
		require.Equal(t, types.CheckFail, c.Status)
		assert.Contains(t, c.Message, "did not return a deny")
		joined := strings.Join(c.Details, "\n")
		assert.Contains(t, joined, "rebuild: magus run build .")
	})

	t.Run("probe passes, no descriptor anywhere -> advice", func(t *testing.T) {
		root := t.TempDir()
		writeGuardProbeStub(t, root, denyingProbeStub)

		c := checkGuardWiring(context.Background(), root, testProbeBudget)
		require.Equal(t, types.CheckAdvice, c.Status)
		assert.Contains(t, c.Message, "no harness descriptor found")
		assert.Contains(t, strings.Join(c.Details, "\n"), "magus\\harness.provider")
	})

	t.Run("probe passes, descriptor verifies its configured adapter -> ok", func(t *testing.T) {
		root := t.TempDir()
		writeGuardProbeStub(t, root, denyingProbeStub)
		writeCheckpointHarness(t, root, guardedHarnessConfig())

		c := checkGuardWiring(context.Background(), root, testProbeBudget, "test-host")
		require.Equal(t, types.CheckOK, c.Status)
		assert.Contains(t, c.Details, filepath.Join(root, "host", "hooks.json"))
	})

	t.Run("probe passes, descriptor exposes a missing adapter -> fail", func(t *testing.T) {
		root := t.TempDir()
		writeGuardProbeStub(t, root, denyingProbeStub)
		writeCheckpointHarness(t, root, `{"hooks":{"before":[{"match":"run","commands":[{"type":"command","command":"other hook"}]}]}}`)

		c := checkGuardWiring(context.Background(), root, testProbeBudget, "test-host")
		require.Equal(t, types.CheckFail, c.Status)
		joined := strings.Join(c.Details, "\n")
		assert.Contains(t, c.Message, "harness wiring is incomplete")
		assert.Contains(t, joined, "missing managed entry")
	})
}

// TestLanguageCoverageRespectsNoLanguage covers both halves of the opt-out: a declared reason
// exempts a spell-less project, and a project that never declared one is still reported. The
// exempt count is in the OK message on purpose, so an exemption stays visible rather than
// disappearing into a green check.
func TestLanguageCoverageRespectsNoLanguage(t *testing.T) {
	var r *runner

	t.Run("a declared reason exempts", func(t *testing.T) {
		got := r.checkLanguageCoverage([]*types.Project{
			{Path: "evals", NoLanguage: "polyglot harness; no single pack describes it"},
			{Path: "api", Spell: "go"},
		})
		assert.Equal(t, types.Check{
			Name:    "language-coverage",
			Status:  types.CheckOK,
			Message: "every project matched a spell or declared no_language (1 exempt)",
		}, got)
	})

	t.Run("an undeclared gap is still reported", func(t *testing.T) {
		got := r.checkLanguageCoverage([]*types.Project{
			{Path: "evals", NoLanguage: "polyglot harness; no single pack describes it"},
			{Path: "forgot-the-import"},
		})
		assert.Equal(t, types.CheckAdvice, got.Status)
		assert.Equal(t, []string{"forgot-the-import"}, got.Details)
	})
}

const (
	testFullHash  = "4cfedce2aa7a510f5fcbd4fd530e8d220edd36be"
	testShortHash = "4cfedce2"
)

func testMeta() types.VCSMeta {
	return types.VCSMeta{ID: testFullHash, Short: testShortHash}
}

// TestContainsShortHash pins the token rule. An abbreviated hash is only a match when it
// stands alone: without the boundary test, every file holding ANY longer hex id whose middle
// happens to spell the abbreviation would report as self-staling, and long hex ids are
// everywhere (lockfile digests, SRI hashes, test fixtures).
func TestContainsShortHash(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want bool
	}{
		{"standalone token", "built from " + testShortHash + " today", true},
		{"at end of file", "commit " + testShortHash, true},
		{"inside a longer hash", "sha256:" + testShortHash + "aa99bb00cc11dd22", false},
		{"preceded by hex", "ff" + testShortHash, false},
		{"absent", "no identifiers here at all", false},
		{"empty body", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, containsShortHash([]byte(tc.body), testShortHash))
		})
	}
}

// TestFileRecordsCommit covers what the scan will and will not read. The skips are the
// difference between a health check and a build step on a workspace with thousands of
// declared outputs.
func TestFileRecordsCommit(t *testing.T) {
	dir := t.TempDir()
	write := func(name string, body []byte) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, body, 0o644))
		return p
	}

	t.Run("full hash matches", func(t *testing.T) {
		p := write("full.html", []byte("<small>Last updated ("+testFullHash+")</small>"))
		assert.True(t, fileRecordsCommit(p, testMeta()))
	})

	t.Run("short hash matches", func(t *testing.T) {
		p := write("short.html", []byte("<small>built "+testShortHash+"</small>"))
		assert.True(t, fileRecordsCommit(p, testMeta()))
	})

	t.Run("a different repository's commit does not match", func(t *testing.T) {
		p := write("lock.json", []byte(`{"rev":"0123456789abcdef0123456789abcdef01234567"}`))
		assert.False(t, fileRecordsCommit(p, testMeta()))
	})

	t.Run("binary content is skipped", func(t *testing.T) {
		p := write("bundle.wasm", append([]byte{0x00, 0x61, 0x73, 0x6d, 0x00}, []byte(testFullHash)...))
		assert.False(t, fileRecordsCommit(p, testMeta()), "a NUL in the first KiB means do not search it")
	})

	t.Run("oversized files are skipped", func(t *testing.T) {
		big := make([]byte, selfStalingMaxFileSize+1)
		for i := range big {
			big[i] = 'a'
		}
		copy(big, []byte(testFullHash))
		p := write("huge.js", big)
		assert.False(t, fileRecordsCommit(p, testMeta()), "past the size cap it is a bundle, not a page")
	})

	t.Run("a missing file is not a finding", func(t *testing.T) {
		assert.False(t, fileRecordsCommit(filepath.Join(dir, "absent.html"), testMeta()))
	})

	t.Run("no commit yet means nothing matches", func(t *testing.T) {
		p := write("empty-meta.html", []byte("Last updated ("+testFullHash+")"))
		assert.False(t, fileRecordsCommit(p, types.VCSMeta{}))
	})
}

// TestDeclaredOutputFiles proves the expansion reads AllOutputs (project-wide PLUS
// per-target), since this workspace declares almost everything per-target with
// ctx.writesFiles; reading only p.Outputs would scan nothing here and report clean.
func TestDeclaredOutputFiles(t *testing.T) {
	dir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "gen"), 0o755))
	for _, name := range []string{"gen/a.html", "gen/b.html", "untouched.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), []byte("x"), 0o644))
	}

	p := &types.Project{
		Path: ".", Dir: dir,
		TargetOutputs: map[string][]types.OutputRef{
			"site-generate": {{Glob: "gen/**"}},
		},
	}
	got := declaredOutputFiles(p)
	assert.Equal(t, []string{"gen/a.html", "gen/b.html"}, got,
		"per-target outputs are expanded; undeclared files are not")

	t.Run("a directory is not a file to scan", func(t *testing.T) {
		p := &types.Project{Path: ".", Dir: dir, Outputs: []string{"gen"}}
		assert.Empty(t, declaredOutputFiles(p))
	})
}

// TestSelfStalingSkipsWithoutTrackedReporter is the guard that keeps this check honest on a
// backend that cannot answer "is this tracked?". Reporting on those would flag the FIXED
// state (a generator still writes the same hash into the same files once they are
// untracked), so the check has to skip instead of guess.
func TestSelfStalingSkipsWithoutTrackedReporter(t *testing.T) {
	// A non-git tree resolves no VCS, which is the same degrade path.
	r := &runner{root: t.TempDir(), ws: stubWorkspace{}}
	got := r.checkSelfStalingOutputs(nil)
	assert.Equal(t, types.CheckOK, got.Status)
	assert.True(t,
		strings.Contains(got.Message, "skipped") ||
			strings.Contains(got.Message, "no VCS") ||
			strings.Contains(got.Message, "no commit"),
		"expected a skip, got %q", got.Message)
}

type stubWorkspace struct{ types.WorkspaceReader }

func (stubWorkspace) VCSOptions() types.VCSOptions { return types.VCSOptions{} }

// seedingRepo commits one declared source, one config nothing declares, and one
// untracked build product. The third is the case the check must NOT report: a fresh
// clone does not have it, so reporting it would make doctor's answer depend on
// whether somebody had built the tree.
func seedingRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(repo, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
		require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		require.NoError(t, cmd.Run(), "git %v", args)
	}
	write("main.go", "package main\n")
	write(".golangci.yml", "linters:\n")
	run("init", "-q", "-b", "main")
	run("add", "-A")
	run("commit", "-qm", "init")
	write("coverage.out", "mode: set\n") // untracked
	return repo
}

func seedingProject() *types.Project {
	return &types.Project{Path: ".", Name: "root", Sources: []string{"**/*.go"}}
}

// TestUndeclaredSeedingFilesReportsTheStandingSet is MGS1028 asked of the whole tree
// rather than of one changeset. The affected-set diagnostic only ever sees the files
// somebody happened to touch, so the declaration that is missing surfaces one pull
// request at a time; this answers it in one pass.
func TestUndeclaredSeedingFilesReportsTheStandingSet(t *testing.T) {
	repo := seedingRepo(t)
	r := &runner{root: repo, ws: stubWorkspace{}}

	got := r.checkUndeclaredSeedingFiles([]*types.Project{seedingProject()})

	require.Equal(t, types.CheckAdvice, got.Status, got.Message)
	assert.Equal(t, []string{".golangci.yml"}, got.Details,
		"main.go is declared, and the untracked coverage.out is a different problem")
	assert.Contains(t, got.Message, "MGS1028")
}

// TestUndeclaredSeedingFilesIsAdviceNotFailure pins the doctrine at types/doctor.go:
// which files are build inputs is the workspace's judgement. A LICENSE nobody's cache
// key reads is CORRECTLY undeclared, and a check that failed on it would be dictating
// a layout rather than reporting a cost.
func TestUndeclaredSeedingFilesIsAdviceNotFailure(t *testing.T) {
	repo := seedingRepo(t)
	r := &runner{root: repo, ws: stubWorkspace{}}

	got := r.checkUndeclaredSeedingFiles([]*types.Project{seedingProject()})

	assert.NotEqual(t, types.CheckFail, got.Status, "this check must never gate a build")
}

// TestUndeclaredSeedingFilesClearsOnceDeclared is the other half of the fix line:
// declaring the file is what makes the check go quiet, so the advice can honestly say
// so.
func TestUndeclaredSeedingFilesClearsOnceDeclared(t *testing.T) {
	repo := seedingRepo(t)
	p := seedingProject()
	p.Sources = []string{"**/*.go", ".golangci.yml"}
	r := &runner{root: repo, ws: stubWorkspace{}}

	got := r.checkUndeclaredSeedingFiles([]*types.Project{p})

	assert.Equal(t, types.CheckOK, got.Status, got.Message)
}

// TestUndeclaredSeedingFilesWithoutVCS degrades rather than guesses: with nothing to
// enumerate committed files with, the check has no candidate set at all, and a walk of
// the working tree would answer a different question (it would count build residue).
func TestUndeclaredSeedingFilesWithoutVCS(t *testing.T) {
	dir := t.TempDir() // not a repository
	r := &runner{root: dir, ws: stubWorkspace{}}

	got := r.checkUndeclaredSeedingFiles([]*types.Project{seedingProject()})

	assert.Equal(t, types.CheckOK, got.Status, got.Message)
}

// TestUnmatchableSourceGlobsReportsPatternsIntoPrunedDirs is MGS1029: the expansion
// walk skips gen/vendor/node_modules/target wholesale, so a PATTERN aimed inside one
// matches nothing and keys nothing, and the target replays while those files change.
func TestUnmatchableSourceGlobsReportsPatternsIntoPrunedDirs(t *testing.T) {
	r := &runner{root: t.TempDir(), ws: stubWorkspace{}}
	p := &types.Project{Path: "docs", Name: "docs", Dir: "docs", Sources: []string{
		"proto/gen/*.binpb",
		"src/**/*.ts",
	}}

	got := r.checkUnmatchableSourceGlobs([]*types.Project{p})

	require.Equal(t, types.CheckFail, got.Status, got.Message)
	require.Len(t, got.Details, 1, "only the glob reaching into gen/ is unmatchable")
	assert.Contains(t, got.Details[0], "proto/gen/*.binpb")
	assert.Contains(t, got.Details[0], `prunes "gen"`)
	assert.Contains(t, got.Message, "MGS1029")
}

// TestUnmatchableSourceGlobsIgnoresExactPaths is the half that must not regress. A
// wildcard-free declaration names ONE file and is resolved by stat rather than the
// walk, so it reaches the cache key from inside a pruned tree normally; reporting it
// would tell the author to fix something that already works.
func TestUnmatchableSourceGlobsIgnoresExactPaths(t *testing.T) {
	r := &runner{root: t.TempDir(), ws: stubWorkspace{}}
	p := &types.Project{Path: "docs", Name: "docs", Dir: "docs", Sources: []string{
		"proto/gen/descriptor.binpb",
		"node_modules/pkg/index.js",
	}}

	got := r.checkUnmatchableSourceGlobs([]*types.Project{p})

	assert.Equal(t, types.CheckOK, got.Status, got.Message)
	assert.Empty(t, got.Details)
}

// TestCheckAgentSkills pins what doctor DOES with the answer; grading an installed copy
// against the binary belongs to internal/agent and is tested there.
func TestCheckAgentSkills(t *testing.T) {
	t.Run("no catalog supplied -> skipped, not a finding", func(t *testing.T) {
		r := &runner{ws: rootStubWorkspace{root: t.TempDir()}}

		got := r.checkAgentSkills()

		assert.Equal(t, types.CheckOK, got.Status)
		assert.Contains(t, got.Message, "skipped")
	})

	t.Run("nothing installed -> advice naming the install command", func(t *testing.T) {
		r := &runner{ws: rootStubWorkspace{root: t.TempDir()}}
		r.opts.skills = agent.NewCatalog(fstest.MapFS{}, "", 1)

		got := r.checkAgentSkills()

		require.Equal(t, types.CheckAdvice, got.Status)
		assert.Contains(t, strings.Join(got.Details, "\n"), "magus agent install")
		assert.Empty(t, got.Fix, "install into WHICH directory is the developer's choice, so there is nothing to apply")
	})

	t.Run("descriptor install -> fix through harness installer", func(t *testing.T) {
		root := t.TempDir()
		writeDoctorHarness(t, root)
		require.NoError(t, os.MkdirAll(filepath.Join(root, ".agents/skills/magus-query"), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(root, ".agents/skills/magus-query/SKILL.md"),
			[]byte("---\nname: magus-query\n---\nold generated body\n"), 0o644))
		r := &runner{ws: rootStubWorkspace{root: root, harnesses: []string{"test-host"}}}
		r.opts.skills = agent.Default(types.KnowledgeSchemaVersion)

		got := r.checkAgentSkills()

		require.Equal(t, types.CheckFail, got.Status)
		assert.Equal(t, []string{"agent", "harness", "install", "--id", "test-host"}, got.Fix)
	})
}

// The case this was written for: the root claims **/*.md through the markdown spell, which sweeps
// in a nested project's generated MAGUS.md. Harmless until the content changes, and then the root
// reports the nested project's own generate as an undeclared source mutation.
func TestOutputIsAnotherProjectsSourceReportsTheOverlap(t *testing.T) {
	r := &runner{root: t.TempDir(), ws: stubWorkspace{}}
	projects := []*types.Project{
		{Path: ".", Name: "root", Sources: []string{"**/*.md"}},
		{Path: "libs/leaf", Name: "leaf", Outputs: []string{"MAGUS.md"}},
	}

	got := r.checkOutputIsAnotherProjectsSource(projects)

	require.Equal(t, types.CheckAdvice, got.Status, got.Message)
	assert.Equal(t, []string{"libs/leaf/MAGUS.md is libs/leaf's output and .'s source"}, got.Details)
}

// A project claiming its OWN output is not the finding: writing what you declared you write is what
// generate is for, and reporting it would make the check fire on every project in the workspace.
func TestOutputIsAnotherProjectsSourceIgnoresAProjectsOwnOutput(t *testing.T) {
	r := &runner{root: t.TempDir(), ws: stubWorkspace{}}
	projects := []*types.Project{
		{Path: "docs", Name: "docs", Sources: []string{"**/*.md"}, Outputs: []string{"MAGUS.md"}},
	}

	got := r.checkOutputIsAnotherProjectsSource(projects)

	assert.Equal(t, types.CheckOK, got.Status, got.Message)
}

// A pattern output is not reported, which is the decidability line MGS4002 draws: whether two globs
// can overlap is undecidable in general, whether a glob matches one literal path is not.
func TestOutputIsAnotherProjectsSourceSkipsPatternOutputs(t *testing.T) {
	r := &runner{root: t.TempDir(), ws: stubWorkspace{}}
	projects := []*types.Project{
		{Path: ".", Name: "root", Sources: []string{"**/*.md"}},
		{Path: "libs/leaf", Name: "leaf", Outputs: []string{"docs/**"}},
	}

	got := r.checkOutputIsAnotherProjectsSource(projects)

	assert.Equal(t, types.CheckOK, got.Status, got.Message)
}

// loadOneEvent puts a single event into the session store for root, dated at.
func loadOneEvent(t *testing.T, root string, at time.Time) {
	t.Helper()
	dir, err := sessions.Dir(root)
	require.NoError(t, err)
	_, err = sessions.LoadEvents(dir, []sessions.LoadEvent{{
		Session: "s1",
		Event: sessions.AgentEvent{
			Host: "h1", Kind: sessions.EventFileRead, Ref: "r1", AtMs: at.UnixMilli(), Text: "a.go",
		},
	}}, sessions.InvocationStart{})
	require.NoError(t, err)
}

func TestCheckSessionLoadStates(t *testing.T) {
	t.Run("never loaded", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())

		got := (&runner{root: t.TempDir()}).checkSessionLoad()

		assert.Equal(t, types.CheckAdvice, got.Status)
		assert.Contains(t, got.Message, "has ever been loaded")
		assert.Contains(t, got.Details[len(got.Details)-1], "magus session load")
	})

	t.Run("stale", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		root := t.TempDir()
		loadOneEvent(t, root, time.Now().Add(-30*24*time.Hour))

		got := (&runner{root: root}).checkSessionLoad()

		assert.Equal(t, types.CheckAdvice, got.Status)
		assert.Contains(t, got.Message, "30 days old")
	})

	t.Run("current", func(t *testing.T) {
		t.Setenv("XDG_STATE_HOME", t.TempDir())
		root := t.TempDir()
		loadOneEvent(t, root, time.Now().Add(-time.Hour))

		got := (&runner{root: root}).checkSessionLoad()

		assert.Equal(t, types.CheckOK, got.Status)
	})
}

// HookConfigs answers for a CHECKOUT, which is the difference that matters to a
// caller reporting on a tree: a machine-wide config in the reader's home says
// nothing about whether the next clone of this repository is wired.
func TestHookConfigsCoversTheCheckoutOnly(t *testing.T) {
	root := t.TempDir()
	writeCheckpointHarness(t, root, guardedHarnessConfig())
	wired := filepath.Join(root, "host", "hooks.json")

	assert.Equal(t, []string{wired}, HookConfigs(context.Background(), root, "test-host"))

	// A config that names magus without running a hook of its own is not wiring: the
	// same two markers the guard-wiring check reads, so neither can count a file the
	// other would not.
	require.NoError(t, os.WriteFile(wired, []byte(`{"note":"magus lives here"}`), 0o644))
	assert.Empty(t, HookConfigs(context.Background(), root, "test-host"))
}

// sameStepFixture is the 2026-09-10 gate stall as a workspace declares it: `ci` composes a
// generator that writes Go files and a badge that reads every Go file. ordered says whether
// the badge declares the ctx.needs edge that was the fix.
func sameStepFixture(ordered bool) *types.Project {
	var badgeChain types.Chain
	if ordered {
		badgeChain = types.Needs("generate")
	}
	return &types.Project{
		Path: ".", Name: "root",
		TargetChains: map[string][]types.ChainStep{
			"ci":             types.Needs("generate", "coverage-badge"),
			"generate":       types.Needs("mocks-generate"),
			"coverage-badge": badgeChain,
		},
		TargetInputs: map[string][]types.InputRef{
			"coverage-badge": {{Project: ".", Glob: "**/*.go"}},
		},
		TargetOutputs: map[string][]types.OutputRef{
			// Not under gen/: a pattern read never hashes a pruned dir, so a file there
			// would witness nothing and the fixture would read as ordered.
			"mocks-generate": {{Project: ".", Glob: "**/mocks/*.go"}},
			"coverage-badge": {{Project: ".", Glob: "assets/coverage.svg"}},
		},
	}
}

// TestSameStepWritesCheck is MGS4008 standing still. A reader that shares a step with the
// writer of the files it reads has to be met at `magus doctor`, where the fix is a line in
// a magusfile, rather than at a gate twenty minutes in.
func TestSameStepWritesCheck(t *testing.T) {
	// A runner with no workspace, not a nil one: the check resolves cross-project chain
	// steps through r.ws, and a fixture that never exercises that would hide the deref.
	// It does have a root, holding one file both globs match: the check refuses only an
	// overlap a file on disk witnesses, so a fixture with no tree would read as clean.
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "internal", "mocks"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "internal", "mocks", "store.go"), []byte("package mocks\n"), 0o644))
	r := &runner{root: root}

	t.Run("an unordered reader fails", func(t *testing.T) {
		got := r.checkSameStepWrites([]*types.Project{sameStepFixture(false)})
		assert.Equal(t, types.CheckFail, got.Status)
		require.Len(t, got.Details, 1)
		assert.Equal(t,
			`root: ci runs . coverage-badge, which reads "**/*.go", alongside . mocks-generate, which writes "**/mocks/*.go", and needs neither from the other (refused at run time)`,
			got.Details[0])
		assert.Contains(t, got.Message, "MGS4008", "the reader gets somewhere to look it up")
	})

	t.Run("the ctx.needs edge silences it", func(t *testing.T) {
		got := r.checkSameStepWrites([]*types.Project{sameStepFixture(true)})
		assert.Equal(t, types.Check{
			Name:    "same-step-writes",
			Status:  types.CheckOK,
			Message: "no composed target runs a reader and a writer of the same files unordered",
		}, got)
	})

	t.Run("a baseline-fallback reader says nothing", func(t *testing.T) {
		p := sameStepFixture(false)
		// No ctx.readsFiles: the badge falls back to the project's source baseline, a
		// whole-project over-approximation, and an overlap through a guess is not a
		// finding a FAIL may rest on.
		p.TargetInputs = nil
		assert.Equal(t, types.CheckOK, r.checkSameStepWrites([]*types.Project{p}).Status)
	})

	t.Run("a project with no composed target is not a project with no answer", func(t *testing.T) {
		got := r.checkSameStepWrites([]*types.Project{{Path: "docs", Name: "docs"}})
		assert.Equal(t, types.CheckOK, got.Status)
	})
}

// TestSameStepWritesCheckIsRegistered pins the wiring, not the predicate: a check nothing
// runs reports on nothing, and the failure mode is silence.
func TestSameStepWritesCheckIsRegistered(t *testing.T) {
	var def *checkDef
	for i := range allChecks {
		if allChecks[i].Name == "same-step-writes" {
			def = &allChecks[i]
		}
	}
	require.NotNil(t, def)
	assert.Equal(t, types.UnorderedSameStepWrite, def.Code)
	assert.True(t, def.NeedsWorkspace, "it reads declarations, which only exist once the workspace loads")
}

// spellWithTool builds a project binding one spell that declares one tool, which is the
// only input MGS1037 reads.
func spellWithTool(tool string, t spells.Tool) []*types.Project {
	return []*types.Project{{Path: ".", Name: "root",
		ResolvedSpells: []*spells.Spell{spells.NewSpell("go", spells.WithTools(map[string]spells.Tool{tool: t}))}}}
}

// TestObservationKeyedAsVersion is MGS1037. The four cases are the four declaration
// shapes that exist in-tree, so each one names the spell it is standing in for.
func TestObservationKeyedAsVersion(t *testing.T) {
	probe := spells.Command{Bin: "govulncheck", Args: []string{"-version"}}

	t.Run("same command, no key -> fail", func(t *testing.T) {
		r := &runner{root: t.TempDir(), ws: stubWorkspace{}}

		got := r.checkObservationKeyedAsVersion(spellWithTool("govulncheck",
			spells.Tool{Probe: probe, Observe: probe}))

		require.Equal(t, types.CheckFail, got.Status, got.Message)
		require.Len(t, got.Details, 1)
		assert.Contains(t, got.Details[0], "govulncheck -version")
		assert.Contains(t, got.Message, "MGS1037")
	})

	t.Run("different args -> ok", func(t *testing.T) {
		// spells/docker: `trivy version` keys the cache, `trivy version --format json`
		// is the observation. One binary, two commands, no leak.
		r := &runner{root: t.TempDir(), ws: stubWorkspace{}}

		got := r.checkObservationKeyedAsVersion(spellWithTool("trivy", spells.Tool{
			Probe:   spells.Command{Bin: "trivy", Args: []string{"version"}},
			Observe: spells.Command{Bin: "trivy", Args: []string{"version", "--format", "json"}},
		}))

		assert.Equal(t, types.CheckOK, got.Status, got.Message)
	})

	t.Run("same command with a key -> ok", func(t *testing.T) {
		r := &runner{root: t.TempDir(), ws: stubWorkspace{}}

		got := r.checkObservationKeyedAsVersion(spellWithTool("govulncheck", spells.Tool{
			Probe: probe, Observe: probe, Key: spells.VersionKey{UpTo: spells.VersionPatch},
		}))

		assert.Equal(t, types.CheckOK, got.Status, got.Message)
	})

	t.Run("observe with no probe -> ok", func(t *testing.T) {
		// The shape the go spell carries today, and the one a check reading only
		// Observe would misreport.
		r := &runner{root: t.TempDir(), ws: stubWorkspace{}}

		got := r.checkObservationKeyedAsVersion(spellWithTool("govulncheck",
			spells.Tool{Observe: probe}))

		assert.Equal(t, types.CheckOK, got.Status, got.Message)
	})
}

func TestCheckJSONCodec(t *testing.T) {
	got := (&runner{}).checkJSONCodec()
	assert.Equal(t, types.CheckOK, got.Status)
	assert.True(t, strings.HasPrefix(got.Message, "encoding/json "), got.Message)
}

// graphStubWorkspace answers Graph() with a fixed error, which is the only thing
// checkGraphCycles reads.
type graphStubWorkspace struct {
	types.WorkspaceReader
	err error
}

func (g graphStubWorkspace) Graph() (*types.Graph, error) { return nil, g.err }

func TestCheckGraphCycles(t *testing.T) {
	ok := (&runner{ws: graphStubWorkspace{}}).checkGraphCycles()
	assert.Equal(t, types.CheckOK, ok.Status)
	assert.Equal(t, "no cycles detected", ok.Message)

	// The graph builder is what detects a cycle, so its error IS the finding and has
	// to reach the report rather than being replaced with a generic message.
	bad := (&runner{ws: graphStubWorkspace{err: errors.New("cycle: a -> b -> a")}}).checkGraphCycles()
	assert.Equal(t, types.CheckFail, bad.Status)
	assert.Equal(t, "cycle: a -> b -> a", bad.Message)
}

// TestCheckConcurrencySizing pins the machine's size through MAGUS_CONCURRENCY so the
// verdict does not depend on the CPU count of whoever runs the suite.
func TestCheckConcurrencySizing(t *testing.T) {
	t.Setenv("MAGUS_CONCURRENCY", "4")

	sized := func(n int) types.Check {
		return (&runner{opts: options{cfg: config.Config{Concurrency: n}}}).checkConcurrencySizing()
	}

	t.Run("unset", func(t *testing.T) {
		got := sized(0)
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Contains(t, got.Message, "unset; sized to this machine (4)")
	})

	t.Run("matches the machine", func(t *testing.T) {
		got := sized(4)
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Contains(t, got.Message, "4, which is what this machine sizes to")
	})

	// Advice, not fail: a deliberately small value is a legitimate choice and magus
	// cannot tell it apart from a stale one.
	t.Run("undersized", func(t *testing.T) {
		got := sized(2)
		assert.Equal(t, types.CheckAdvice, got.Status)
		assert.Contains(t, got.Message, "undersized")
		assert.Contains(t, got.Message, "leaves capacity idle")
		assert.Equal(t, []string{"config", "set", "key=concurrency,value=4"}, got.Fix)
	})

	// The worse direction: the work still completes, just slower, so nothing ever
	// points at the cause.
	t.Run("oversized", func(t *testing.T) {
		got := sized(16)
		assert.Equal(t, types.CheckAdvice, got.Status)
		assert.Contains(t, got.Message, "oversized")
		assert.Contains(t, got.Message, "contend rather than finish sooner")
		assert.Equal(t, []string{"config", "set", "key=concurrency,value=4"}, got.Fix)
	})
}

func TestCheckWorkspaceRegistration(t *testing.T) {
	loaded := time.Now().Add(-90 * time.Second)

	t.Run("no server", func(t *testing.T) {
		got := (&runner{}).checkWorkspaceRegistration()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Equal(t, "no loaded workspaces in server", got.Message)
	})

	t.Run("server reachable but holding nothing", func(t *testing.T) {
		r := &runner{opts: options{serverInfo: &ServerInfo{Reachable: true}}}
		assert.Equal(t, "no loaded workspaces in server", r.checkWorkspaceRegistration().Message)
	})

	t.Run("unreachable server", func(t *testing.T) {
		r := &runner{opts: options{serverInfo: &ServerInfo{
			Workspaces: []LoadedWorkspace{{Root: "/repo", LastAccess: loaded}},
		}}}
		assert.Equal(t, "no loaded workspaces in server", r.checkWorkspaceRegistration().Message)
	})

	t.Run("registered", func(t *testing.T) {
		r := &runner{root: "/repo", opts: options{serverInfo: &ServerInfo{
			Reachable:  true,
			Workspaces: []LoadedWorkspace{{Root: "/repo", LastAccess: loaded}, {Root: "/other", LastAccess: loaded}},
		}}}
		got := r.checkWorkspaceRegistration()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Contains(t, got.Message, "loaded in server")
		assert.Contains(t, got.Message, "(2 workspace(s) total)")
		require.Len(t, got.Details, 2)
		assert.Contains(t, got.Details[0], "/repo")
		assert.Contains(t, got.Details[0], "idle ")
	})

	// Not yet loaded is normal (a workspace loads on first use), so this stays OK
	// and only says what it sees.
	t.Run("not registered", func(t *testing.T) {
		r := &runner{root: "/repo", opts: options{serverInfo: &ServerInfo{
			Reachable:  true,
			Workspaces: []LoadedWorkspace{{Root: "/elsewhere", LastAccess: loaded}},
		}}}
		got := r.checkWorkspaceRegistration()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Contains(t, got.Message, "not yet loaded in server")
	})

	// The server passes the workspace through r.ws, leaving r.root empty on that path.
	t.Run("root comes from the workspace when set", func(t *testing.T) {
		r := &runner{ws: rootStubWorkspace{root: "/repo"}, opts: options{serverInfo: &ServerInfo{
			Reachable:  true,
			Workspaces: []LoadedWorkspace{{Root: "/repo", LastAccess: loaded}},
		}}}
		assert.Contains(t, r.checkWorkspaceRegistration().Message, "loaded in server")
	})
}

func TestSockDirOrDefault(t *testing.T) {
	var absent *ServerInfo
	assert.Equal(t, "", absent.sockDirOrDefault())
	assert.Equal(t, "", (&ServerInfo{}).sockDirOrDefault())
	assert.Equal(t, "/run/magus", (&ServerInfo{SockDir: "/run/magus"}).sockDirOrDefault())
}

// listenUnix opens a real socket so the dial probe has something live to find. macOS
// caps a Unix socket path near 104 bytes and a temp dir can exceed it, so a failure to
// bind is reported as an environment skip rather than as a defect in the check.
func listenUnix(t *testing.T, path string) {
	t.Helper()
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Skipf("cannot bind a unix socket at %s: %v", path, err)
	}
	t.Cleanup(func() { _ = ln.Close() })
}

func TestIsSocketAlive(t *testing.T) {
	dir := t.TempDir()

	dead := filepath.Join(dir, "dead.sock")
	require.NoError(t, os.WriteFile(dead, nil, 0o644))
	assert.False(t, isSocketAlive(context.Background(), dead), "a plain file is not a listener")
	assert.False(t, isSocketAlive(context.Background(), filepath.Join(dir, "absent.sock")))

	live := filepath.Join(dir, "live.sock")
	listenUnix(t, live)
	assert.True(t, isSocketAlive(context.Background(), live))
}

func TestCheckStaleSockets(t *testing.T) {
	t.Run("no socket directory configured", func(t *testing.T) {
		got := (&runner{}).checkStaleSockets()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Equal(t, "no socket directory", got.Message)
	})

	t.Run("socket directory does not exist", func(t *testing.T) {
		r := &runner{opts: options{serverInfo: &ServerInfo{SockDir: filepath.Join(t.TempDir(), "absent")}}}
		got := r.checkStaleSockets()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Equal(t, "no socket directory", got.Message)
	})

	// named is a ServerInfo carrying the socket names the CLI passes, over dir.
	named := func(dir string) *ServerInfo {
		return &ServerInfo{SockDir: dir, ServerSocket: "server.sock", BrokerSocket: "broker.sock"}
	}

	t.Run("empty directory", func(t *testing.T) {
		got := (&runner{opts: options{serverInfo: named(t.TempDir())}}).checkStaleSockets()
		assert.Equal(t, types.Check{
			Name: "sockets", Status: types.CheckOK,
			Message: "server not running, broker not running, 0 per-process pool(s)",
		}, got)
	})

	// The regression: the check globbed magus-*.sock only, so the server and the broker,
	// on their fixed names, were invisible to it.
	t.Run("the server and the broker are reported by role", func(t *testing.T) {
		dir := t.TempDir()
		listenUnix(t, filepath.Join(dir, "server.sock"))
		listenUnix(t, filepath.Join(dir, "broker.sock"))
		listenUnix(t, filepath.Join(dir, "magus-41221-abc.sock"))

		got := (&runner{opts: options{serverInfo: named(dir)}}).checkStaleSockets()
		assert.Equal(t, types.Check{
			Name: "sockets", Status: types.CheckOK,
			Message: "server live, broker live, 1 per-process pool(s)",
		}, got)
	})

	// Leftover dead sockets are harmless cruft (each is reclaimed on the next bind), so
	// they are context rather than a failure. Anything not named magus-*.sock or one of the
	// well-known names, and any directory, is not ours.
	t.Run("stale sockets are reported, not failed", func(t *testing.T) {
		dir := t.TempDir()
		for _, name := range []string{"magus-a.sock", "server.sock", "broker.sock", "other.sock", "magus-notasocket"} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, name), nil, 0o644))
		}
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "magus-dir.sock"), 0o755))

		got := (&runner{opts: options{serverInfo: named(dir)}}).checkStaleSockets()
		assert.Equal(t, types.Check{
			Name: "sockets", Status: types.CheckOK,
			Message: "server stale, broker stale, 0 per-process pool(s), 3 stale socket(s)",
			Details: []string{
				"stale: " + filepath.Join(dir, "broker.sock"),
				"stale: " + filepath.Join(dir, "magus-a.sock"),
				"stale: " + filepath.Join(dir, "server.sock"),
			},
		}, got)
	})

	// Two live servers is a real conflict, and the only shape that fails: one on
	// server.sock and one on a configured address inside the same directory.
	t.Run("two live servers", func(t *testing.T) {
		dir := t.TempDir()
		listenUnix(t, filepath.Join(dir, "server.sock"))
		listenUnix(t, filepath.Join(dir, "magus-custom.sock"))

		got := (&runner{opts: options{serverInfo: named(dir)}}).checkStaleSockets()
		assert.Equal(t, types.Check{
			Name: "sockets", Status: types.CheckFail,
			Message: "2 live server sockets: more than one server is running",
			Details: []string{
				"live: " + filepath.Join(dir, "server.sock"),
				"live: " + filepath.Join(dir, "magus-custom.sock"),
			},
		}, got)
	})

	// A server plus a run in flight is the ORDINARY state: a run hosts its own pool for
	// its children. Counting the pool as a server reported that state as a conflict.
	t.Run("a live per-process pool beside the server is not a conflict", func(t *testing.T) {
		dir := t.TempDir()
		listenUnix(t, filepath.Join(dir, "server.sock"))
		listenUnix(t, filepath.Join(dir, "magus-41221-abc.sock"))

		got := (&runner{opts: options{serverInfo: named(dir)}}).checkStaleSockets()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Equal(t, "server live, broker not running, 1 per-process pool(s)", got.Message)
	})
}

func TestCheckStaleShadowAcks(t *testing.T) {
	acks := config.Config{Spells: config.SpellsConfig{AllowShadow: []config.ShadowAck{
		{Name: "spells/hello", Reason: "the nested copy is deliberate"},
		{Name: "spells/world", Reason: "ditto"},
	}}}

	t.Run("nothing acknowledged", func(t *testing.T) {
		got := (&runner{}).checkStaleShadowAcks()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Equal(t, "no allow_shadow entries", got.Message)
	})

	t.Run("workspace not loaded", func(t *testing.T) {
		got := (&runner{opts: options{cfg: acks}}).checkStaleShadowAcks()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Equal(t, "workspace not loaded", got.Message)
	})

	// An acknowledgment whose shadow is gone is dead config: the reason it carries no
	// longer describes anything, which is what keeps the opt-out list meaningful.
	t.Run("every ack is stale", func(t *testing.T) {
		r := &runner{ws: rootStubWorkspace{root: t.TempDir()}, opts: options{cfg: acks}}
		got := r.checkStaleShadowAcks()
		assert.Equal(t, types.CheckFail, got.Status)
		assert.Contains(t, got.Message, "2 allow_shadow entr(ies) no longer match a real shadow")
		require.Len(t, got.Details, 2)
		// Sorted, so the report does not reorder between runs over the same config.
		assert.Contains(t, got.Details[0], `"spells/hello" no longer shadows anything`)
		assert.Contains(t, got.Details[0], "the nested copy is deliberate")
		assert.Contains(t, got.Details[1], `"spells/world"`)
	})
}

// plant writes a file under root, creating its parents, and returns the path.
func plant(t *testing.T, root, rel, body string) string {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

// touchAt backdates or advances a path's mtime, which is the whole input to the
// staleness comparison.
func touchAt(t *testing.T, path string, at time.Time) {
	t.Helper()
	require.NoError(t, os.Chtimes(path, at, at))
}

// withoutPathMagus empties PATH so exec.LookPath("magus") cannot resolve, which is what
// makes "no guard at all" reachable on a developer machine that has one installed.
func withoutPathMagus(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

func TestNewestGoSource(t *testing.T) {
	root := t.TempDir()
	base := time.Now().Add(-24 * time.Hour)

	touchAt(t, plant(t, root, "old.go", "package a\n"), base)
	newest := plant(t, root, "sub/new.go", "package b\n")
	touchAt(t, newest, base.Add(time.Hour))
	// Not Go source, so its mtime must not win.
	touchAt(t, plant(t, root, "README.md", "hi\n"), base.Add(10*time.Hour))
	// The directories that never hold guard sources, each planted with a file newer
	// than everything else so a missing skip shows up as the wrong answer.
	for _, dir := range []string{".git", "node_modules", "gen", ".claude"} {
		touchAt(t, plant(t, root, dir+"/skipped.go", "package c\n"), base.Add(20*time.Hour))
	}

	at, path := newestGoSource(root)
	assert.Equal(t, filepath.FromSlash("sub/new.go"), path)
	assert.WithinDuration(t, base.Add(time.Hour), at, time.Second)
}

func TestNewestGoSourceWithoutGoFiles(t *testing.T) {
	at, path := newestGoSource(t.TempDir())
	assert.True(t, at.IsZero())
	assert.Equal(t, "", path)
}

func TestCheckGuardBinary(t *testing.T) {
	// A stale guard is worse than an absent one: an absent guard is noticed within a
	// command or two, a stale one is trusted indefinitely.
	t.Run("older than the working tree", func(t *testing.T) {
		root := t.TempDir()
		now := time.Now()
		touchAt(t, plant(t, root, "main.go", "package main\n"), now)
		bin := plant(t, root, "magus", "#!/bin/sh\n")
		require.NoError(t, os.Chmod(bin, 0o755))
		touchAt(t, bin, now.Add(-time.Hour))

		got := (&runner{ws: rootStubWorkspace{root: root}}).checkGuardBinary()
		assert.Equal(t, types.CheckFail, got.Status)
		assert.Contains(t, got.Message, "stale rules")
		require.Len(t, got.Details, 3)
		assert.Contains(t, got.Details[1], filepath.FromSlash("main.go"))
		assert.Contains(t, got.Details[2], "rebuild:")
	})

	t.Run("newer than every tracked Go source", func(t *testing.T) {
		root := t.TempDir()
		now := time.Now()
		touchAt(t, plant(t, root, "main.go", "package main\n"), now.Add(-time.Hour))
		bin := plant(t, root, "magus", "#!/bin/sh\n")
		require.NoError(t, os.Chmod(bin, 0o755))
		touchAt(t, bin, now)

		got := (&runner{ws: rootStubWorkspace{root: root}}).checkGuardBinary()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Contains(t, got.Message, "hook would run ./magus")
	})

	// The resolved path is reported always, not only on failure: "which binary is
	// judging me?" has no other way to be asked.
	t.Run("falls back to PATH", func(t *testing.T) {
		dir := t.TempDir()
		fake := filepath.Join(dir, "magus")
		require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755))
		t.Setenv("PATH", dir)

		got := (&runner{ws: rootStubWorkspace{root: t.TempDir()}}).checkGuardBinary()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Contains(t, got.Message, "no ./magus built")
		assert.Contains(t, got.Message, fake)
	})

	t.Run("no binary anywhere", func(t *testing.T) {
		withoutPathMagus(t)
		got := (&runner{ws: rootStubWorkspace{root: t.TempDir()}}).checkGuardBinary()
		assert.Equal(t, types.CheckFail, got.Status)
		assert.Contains(t, got.Message, "a guard hook is unenforced")
		assert.Equal(t, []string{"build one: magus run build ."}, got.Details)
	})

	// A non-executable ./magus is not a binary a hook can run, so resolution has to
	// carry on to PATH rather than stopping at the name.
	t.Run("non-executable ./magus", func(t *testing.T) {
		withoutPathMagus(t)
		root := t.TempDir()
		plant(t, root, "magus", "not a binary\n")

		got := (&runner{ws: rootStubWorkspace{root: root}}).checkGuardBinary()
		assert.Equal(t, types.CheckFail, got.Status)
		assert.Contains(t, got.Message, "no ./magus and no magus on PATH")
	})
}

// TestResolveGuardBinaryForWiring is kept separate from checkGuardBinary on purpose, so
// a change to one check's resolution order cannot silently retarget the other's probe.
func TestResolveGuardBinaryForWiring(t *testing.T) {
	t.Run("prefers ./magus", func(t *testing.T) {
		root := t.TempDir()
		bin := plant(t, root, "magus", "#!/bin/sh\n")
		require.NoError(t, os.Chmod(bin, 0o755))

		got, ok := resolveGuardBinaryForWiring(root)
		require.True(t, ok)
		assert.Equal(t, bin, got)
	})

	t.Run("falls back to PATH", func(t *testing.T) {
		dir := t.TempDir()
		fake := filepath.Join(dir, "magus")
		require.NoError(t, os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755))
		t.Setenv("PATH", dir)

		got, ok := resolveGuardBinaryForWiring(t.TempDir())
		require.True(t, ok)
		assert.Equal(t, fake, got)
	})

	t.Run("nothing to resolve", func(t *testing.T) {
		withoutPathMagus(t)
		_, ok := resolveGuardBinaryForWiring(t.TempDir())
		assert.False(t, ok)
	})

	t.Run("a directory named magus is not a binary", func(t *testing.T) {
		withoutPathMagus(t)
		root := t.TempDir()
		require.NoError(t, os.MkdirAll(filepath.Join(root, "magus"), 0o755))

		_, ok := resolveGuardBinaryForWiring(root)
		assert.False(t, ok)
	})
}

func TestHarnessConfigCandidates(t *testing.T) {
	root := t.TempDir()
	writeDoctorHarness(t, root)

	got, err := harnessConfigCandidates(root, "test-host")
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(root, "host", "hooks.json")}, got)
}

func TestGuardReferencedTemplates(t *testing.T) {
	root := t.TempDir()
	configDir := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(configDir, 0o755))

	fromRoot := plant(t, root, "docs/guides/magus-command.sh", "#!/bin/sh\n")
	besideConfig := plant(t, configDir, "magus-path.sh", "#!/bin/sh\n")

	t.Run("resolves against the root and against the config dir", func(t *testing.T) {
		body := []byte(`{"command": "sh docs/guides/magus-command.sh", "other": "magus-path.sh"}`)
		found, missing := guardReferencedTemplates(root, configDir, body)
		assert.Equal(t, []string{fromRoot, besideConfig}, found)
		assert.Empty(t, missing)
	})

	t.Run("resolves Codex's VCS-neutral workspace-root shell expansion", func(t *testing.T) {
		body := []byte(`{"command": "sh \"$(magus describe projects -o 'template={{.workspace}}')/docs/guides/magus-command.sh\""}`)
		found, missing := guardReferencedTemplates(root, configDir, body)
		assert.Equal(t, []string{fromRoot}, found)
		assert.Empty(t, missing)
	})

	// Root-relative treatment is deliberately a narrow compatibility rule for the
	// documented expansion, not a way for a config to smuggle an arbitrary shell
	// command into doctor.
	t.Run("does not resolve an arbitrary magus shell expansion from the root", func(t *testing.T) {
		body := []byte(`{"command": "sh \"$(magus version)/docs/guides/magus-command.sh\""}`)
		found, missing := guardReferencedTemplates(root, configDir, body)
		assert.Empty(t, found)
		assert.NotEmpty(t, missing)
	})

	// A config whose hook points at a template that is not there runs nothing;
	// reporting only what resolved would grade exactly that case as healthy.
	t.Run("an unresolvable token is the finding", func(t *testing.T) {
		body := []byte(`{"command": "sh hooks/cursor-hook.sh"}`)
		found, missing := guardReferencedTemplates(root, configDir, body)
		assert.Empty(t, found)
		assert.Equal(t, []string{"hooks/cursor-hook.sh"}, missing, "the token is reported as the config wrote it")
	})

	t.Run("a config naming no template", func(t *testing.T) {
		found, missing := guardReferencedTemplates(root, configDir, []byte(`{"hooks": []}`))
		assert.Empty(t, found)
		assert.Empty(t, missing)
	})

	// The token starts at the nearest quote, space, tab, newline or '=', so a bare
	// basename is taken whole rather than swallowing the word before it.
	t.Run("a bare basename", func(t *testing.T) {
		bare := plant(t, root, "cursor-hook.sh", "#!/bin/sh\n")
		found, missing := guardReferencedTemplates(root, configDir, []byte("hook=cursor-hook.sh\n"))
		assert.Equal(t, []string{bare}, found)
		assert.Empty(t, missing)
	})
}

func TestGuardTemplateMarkerProblem(t *testing.T) {
	marker := agent.GuardTemplateMarker

	t.Run("current", func(t *testing.T) {
		body := []byte("#!/bin/sh\n# " + marker + " " + strconv.Itoa(agent.GuardTemplateVersion) + "\n")
		assert.Equal(t, "", guardTemplateMarkerProblem(body))
	})

	t.Run("ahead of this binary", func(t *testing.T) {
		body := []byte("# " + marker + " " + strconv.Itoa(agent.GuardTemplateVersion+1) + "\n")
		assert.Equal(t, "", guardTemplateMarkerProblem(body))
	})

	t.Run("behind", func(t *testing.T) {
		body := []byte("# " + marker + " 1\n")
		got := guardTemplateMarkerProblem(body)
		assert.Contains(t, got, "template version 1 is older than current version "+strconv.Itoa(agent.GuardTemplateVersion))
		assert.Contains(t, got, "re-download it")
	})

	// A MISSING marker is a finding, not a pass: the marker postdates the templates,
	// so a copy carrying none is older than versioning. Measured on a real machine, a
	// plugin still calling a removed subcommand graded healthy without this.
	t.Run("no marker at all", func(t *testing.T) {
		got := guardTemplateMarkerProblem([]byte("#!/bin/sh\necho hi\n"))
		assert.Contains(t, got, "carries no "+marker+" line")
		assert.Contains(t, got, "predates template versioning")
	})

	// An unreadable version is treated as current rather than as a version-0 copy:
	// the marker is there, so the file is not from before versioning.
	t.Run("unparsable version", func(t *testing.T) {
		assert.Equal(t, "", guardTemplateMarkerProblem([]byte("# "+marker+" seven\n")))
	})

	// The marker on the last line, with no newline after it.
	t.Run("marker at end of file", func(t *testing.T) {
		assert.Equal(t, "", guardTemplateMarkerProblem([]byte("# "+marker+" "+strconv.Itoa(agent.GuardTemplateVersion))))
	})
}

func TestCheckObserverRecording(t *testing.T) {
	// observe appends n hook observations of one tool into the workspace's trail.
	observe := func(t *testing.T, root, tool string, n int) {
		t.Helper()
		base := (&runner{root: root}).cacheDir()
		for i := range n {
			trail.AppendAgentCommand(context.Background(), base, trail.AgentCommand{
				Host: "test",
				Tool: tool,
				Path: fmt.Sprintf("file-%d.go", i),
			})
		}
	}

	// A workspace no agent has run in is the ordinary case, and failing it would train
	// people to ignore the check.
	t.Run("nothing recorded", func(t *testing.T) {
		got := (&runner{root: t.TempDir()}).checkObserverRecording()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Contains(t, got.Message, "no agent activity recorded yet")
	})

	// Below the sample floor a handful of events with no reads is what a fixture or
	// one session looks like; judging it would be this check making the exact mistake
	// it exists to catch.
	t.Run("too few to judge", func(t *testing.T) {
		root := t.TempDir()
		observe(t, root, "file.write", observerMinSample-1)

		got := (&runner{root: root}).checkObserverRecording()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Contains(t, got.Message, "too few to judge")
		assert.Contains(t, got.Message, strconv.Itoa(observerMinSample-1)+" observation(s)")
	})

	// Commands with no reads is the diagnostic pattern: wiring correct, doctor green,
	// and the story behind a change unreconstructable.
	t.Run("not one read", func(t *testing.T) {
		root := t.TempDir()
		observe(t, root, "file.write", 20)
		observe(t, root, "shell.command", 40)

		got := (&runner{root: root}).checkObserverRecording()
		assert.Equal(t, types.CheckFail, got.Status)
		assert.Contains(t, got.Message, "NOT ONE read")
		require.NotEmpty(t, got.Details)
		assert.Contains(t, got.Details[0], "writes: 20")
		assert.Contains(t, got.Details[0], "shell: 40")
		assert.Contains(t, got.Details[0], "reads: 0")
	})

	// Wired, recording, and still useless for its purpose: a diff can name the agent
	// that wrote a file but not what it had just read.
	t.Run("sparse reading trail", func(t *testing.T) {
		root := t.TempDir()
		observe(t, root, "file.read", 5)
		observe(t, root, "file.write", 60)

		got := (&runner{root: root}).checkObserverRecording()
		assert.Equal(t, types.CheckAdvice, got.Status)
		assert.Contains(t, got.Message, "too sparse to explain a change")
		assert.Contains(t, got.Message, "5 read(s) against 60 write(s)")
	})

	t.Run("recording healthily", func(t *testing.T) {
		root := t.TempDir()
		observe(t, root, "file.read", 60)
		observe(t, root, "file.write", 10)

		got := (&runner{root: root}).checkObserverRecording()
		assert.Equal(t, types.CheckOK, got.Status)
		assert.Contains(t, got.Message, "recording: 60 read(s), 10 write(s)")
	})
}

func TestCacheDir(t *testing.T) {
	assert.Equal(t, filepath.Join("/repo", ".magus"), (&runner{root: "/repo"}).cacheDir())

	abs := &runner{root: "/repo", opts: options{cfg: config.Config{Cache: config.Cache{Dir: "/var/cache/magus/"}}}}
	assert.Equal(t, filepath.FromSlash("/var/cache/magus"), abs.cacheDir())

	rel := &runner{root: "/repo", opts: options{cfg: config.Config{Cache: config.Cache{Dir: "build/cache"}}}}
	assert.Equal(t, filepath.Join("/repo", "build", "cache"), rel.cacheDir())
}

func TestFirstExistingConfig(t *testing.T) {
	assert.Equal(t, "", firstExistingConfig(t.TempDir()))

	dotted := t.TempDir()
	want := plant(t, dotted, ".magus.yaml", "log:\n")
	assert.Equal(t, want, firstExistingConfig(dotted))

	// magus.yaml wins when both exist, matching the loader's own order.
	both := t.TempDir()
	plain := plant(t, both, "magus.yaml", "log:\n")
	plant(t, both, ".magus.yaml", "log:\n")
	assert.Equal(t, plain, firstExistingConfig(both))
}

func TestConfigFilePaths(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	root := t.TempDir()
	want := plant(t, root, "magus.yaml", "log:\n")

	got := configFilePaths(root)
	assert.Contains(t, got, want)
	// Only files that exist: every path returned is handed straight to config.LoadFile,
	// and a missing one would be reported as a config problem the user does not have.
	for _, p := range got {
		_, err := os.Stat(p)
		assert.NoError(t, err, p)
	}

	// An empty root is the server's path, where the workspace arrives through r.ws.
	assert.NotPanics(t, func() { configFilePaths("") })
}

func TestNameConvention(t *testing.T) {
	cases := []struct{ name, want string }{
		{"build", ""},
		{"", ""},
		{"no_cache", "snake_case"},
		{"buildAll", "camelCase"},
		{"BuildAll", "PascalCase"},
		{"Build", "PascalCase"},
		// A delimiter wins over casing: snake_case is the stronger signal.
		{"Build_All", "snake_case"},
	}
	for _, c := range cases {
		assert.Equal(t, c.want, nameConvention(c.name), "nameConvention(%q)", c.name)
	}
}

func TestEscapesRoot(t *testing.T) {
	assert.False(t, escapesRoot(""), "many node kinds put a name rather than a path here")
	assert.False(t, escapesRoot("internal/doctor/checks.go"))
	assert.False(t, escapesRoot("a..b"), "a name containing dots is not a parent segment")
	assert.True(t, escapesRoot("../elsewhere/x.go"))
	assert.True(t, escapesRoot("a/../../b"))
}

func TestToSlashRel(t *testing.T) {
	root := filepath.FromSlash("/repo")
	assert.Equal(t, "internal/doctor/checks.go",
		toSlashRel(root, filepath.Join(root, "internal", "doctor", "checks.go")))
	// filepath.Rel fails against an empty root, and the absolute path is better than
	// a detail naming no file at all.
	assert.Equal(t, filepath.FromSlash("/repo/x.go"), toSlashRel("", filepath.FromSlash("/repo/x.go")))
}

func TestSymlinkEscapes(t *testing.T) {
	// EvalSymlinks first: macOS's TempDir sits under the /var -> /private/var link, and
	// an unresolved root makes every in-tree target read as an escape.
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	outside, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	plant(t, root, "inside.txt", "hi\n")
	plant(t, outside, "outside.txt", "hi\n")

	t.Run("in-tree", func(t *testing.T) {
		link := filepath.Join(root, "in.link")
		require.NoError(t, os.Symlink(filepath.Join(root, "inside.txt"), link))
		_, escapes := symlinkEscapes(root, link)
		assert.False(t, escapes)
	})

	// The sandbox-escape vector this check exists for, where landlock is unavailable.
	t.Run("escaping", func(t *testing.T) {
		link := filepath.Join(root, "out.link")
		require.NoError(t, os.Symlink(filepath.Join(outside, "outside.txt"), link))
		target, escapes := symlinkEscapes(root, link)
		assert.True(t, escapes)
		assert.Contains(t, target, "outside.txt")
	})

	// EvalSymlinks fails on a dangling link, so direction is judged lexically instead
	// of the link being waved through.
	t.Run("dangling relative link", func(t *testing.T) {
		link := filepath.Join(root, "dangling.link")
		require.NoError(t, os.Symlink("../gone.txt", link))
		_, escapes := symlinkEscapes(root, link)
		assert.True(t, escapes)
	})

	t.Run("dangling in-tree link", func(t *testing.T) {
		link := filepath.Join(root, "dangling-inside.link")
		require.NoError(t, os.Symlink("gone.txt", link))
		_, escapes := symlinkEscapes(root, link)
		assert.False(t, escapes)
	})
}

func TestPrunedPrefix(t *testing.T) {
	// Nobody writes "gen/*.binpb" hoping it matches nothing.
	for _, glob := range []string{"gen/*.binpb", "./gen/**/*.go", "node_modules/**/*.js", "vendor/*"} {
		_, ok := prunedPrefix(glob)
		assert.True(t, ok, "prunedPrefix(%q)", glob)
	}
	dir, ok := prunedPrefix("gen/*.binpb")
	require.True(t, ok)
	assert.Equal(t, "gen", dir)

	// A wildcard-free path names one file and is resolved by stat, so it reaches the
	// key from inside a pruned tree normally.
	for _, glob := range []string{"gen/knowledge-graph.json", "**/*.go", "internal/**/*.go", "src/*.ts"} {
		_, ok := prunedPrefix(glob)
		assert.False(t, ok, "prunedPrefix(%q)", glob)
	}
}

// TestPrunedPrefixIgnoresARelativePrefix keeps "." and ".." out of the segment scan,
// which would otherwise never match an ignore dir but would cost a lookup each.
func TestPrunedPrefixIgnoresARelativePrefix(t *testing.T) {
	dir, ok := prunedPrefix("../gen/*.go")
	require.True(t, ok)
	assert.Equal(t, "gen", dir)
}

// TestGuardTemplateBasenamesAreShipped pins the list against the templates that actually
// exist, because for the whole life of one rename it named magus-pause.sh, a file the
// same commit had renamed to magus-checkpoint.sh.
//
// The cost of that is total and silent: guardReferencedTemplates only inspects a config
// for basenames in this list, so the one check written to catch a silently stale hook
// could not match the only name a config ever carries. A wired host graded healthy while
// running a script that invoked a subcommand magus no longer has.
//
// Membership is deliberately NOT asserted in the other direction: a template a host
// discovers by placing it in a directory is graded in checkGuardWiring's directory
// branch and correctly absent here (magus-observe.sh is the standing example).
func TestGuardTemplateBasenamesAreShipped(t *testing.T) {
	dir := filepath.Join("..", "..", "docs", "guides", "integrations", "agents")
	for _, base := range guardTemplateBasenames {
		assert.FileExistsf(t, filepath.Join(dir, base),
			"guardTemplateBasenames names %q, which this repo does not ship; a config can never carry that name, so the check silently matches nothing", base)
	}
}
