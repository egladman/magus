package doctor

import (
	"context"
	"fmt"
	"github.com/egladman/magus/internal/agent"
	"github.com/egladman/magus/internal/cache"
	"github.com/egladman/magus/internal/json"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
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

	assert.Equal(t, types.DoctorCheck{
		Name:    "cache yield",
		Status:  types.DoctorOK,
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

	require.Equal(t, types.DoctorFail, got.Status)
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

	require.Equal(t, types.DoctorFail, got.Status)
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

	assert.Equal(t, types.DoctorCheck{
		Name:    "dead output globs",
		Status:  types.DoctorOK,
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

	assert.Equal(t, types.DoctorFail, got.Status)
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

	assert.Equal(t, types.DoctorFail, got.Status)
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
		assert.Equal(t, types.DoctorFail, got.Status)
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
		assert.Equal(t, types.DoctorCheck{
			Name:    "output ownership",
			Status:  types.DoctorOK,
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
		assert.Equal(t, types.DoctorOK, got.Status)
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

// writeGuardCanaryStub writes root/magus as a stub binary standing in for the
// real one: the canary only cares that `magus session hook -o name` prints a decision
// on stdout and exits accordingly, never about actual guard rules.
func writeGuardCanaryStub(t *testing.T, root, body string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(root, "magus"), []byte(body), 0o755))
}

const denyingCanaryStub = "#!/bin/sh\necho deny\nexit 2\n"

// testCanaryBudget is far above the shipped guardCanaryBudget on purpose.
// These subtests exec a real child process, and this repo is developed across
// many concurrent worktrees - a machine busy enough to push a trivial exec
// past the production budget would make this suite flaky for a reason that
// says nothing about the check. The production value stays 5s; only the test
// waits longer.
const testCanaryBudget = 60 * time.Second

func TestCheckGuardWiring(t *testing.T) {
	t.Run("no binary resolves at all -> fail", func(t *testing.T) {
		root := t.TempDir()
		t.Setenv("PATH", t.TempDir()) // empty: no magus anywhere
		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorFail, c.Status)
		assert.Contains(t, c.Message, "no ./magus and no magus on PATH")
	})

	t.Run("canary does not return a deny -> fail with observed output", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, "#!/bin/sh\nexit 0\n")

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorFail, c.Status)
		assert.Contains(t, c.Message, "did not return a deny")
		joined := strings.Join(c.Details, "\n")
		assert.Contains(t, joined, "rebuild: magus run build .")
	})

	t.Run("canary passes, no config anywhere -> advice naming the guide", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, denyingCanaryStub)

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorAdvice, c.Status)
		assert.Contains(t, c.Message, "no agent-host hook config found")
		assert.Contains(t, strings.Join(c.Details, "\n"), "docs/guides/integrations/agents.md")
	})

	t.Run("canary passes, config mentions magus and hook with no template reference -> ok", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, denyingCanaryStub)

		settingsDir := filepath.Join(root, ".claude")
		require.NoError(t, os.MkdirAll(settingsDir, 0o755))
		settingsPath := filepath.Join(settingsDir, "settings.json")
		require.NoError(t, os.WriteFile(settingsPath,
			[]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"magus session hook -o json"}]}]}}`), 0o600))

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorOK, c.Status)
		assert.Contains(t, c.Details, settingsPath)
	})

	t.Run("canary passes, referenced template carries a stale marker -> fail naming path and fix", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, denyingCanaryStub)

		tmplDir := filepath.Join(root, "docs", "guides", "integrations", "agents")
		require.NoError(t, os.MkdirAll(tmplDir, 0o755))
		staleVersion := agent.GuardTemplateVersion - 1
		tmplPath := filepath.Join(tmplDir, "magus-guard-command.sh")
		require.NoError(t, os.WriteFile(tmplPath,
			[]byte(fmt.Sprintf("#!/usr/bin/env sh\n# %s %d\n", agent.GuardTemplateMarker, staleVersion)), 0o644))

		settingsDir := filepath.Join(root, ".claude")
		require.NoError(t, os.MkdirAll(settingsDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(settingsDir, "settings.json"),
			[]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"sh docs/guides/integrations/agents/magus-guard-command.sh"}]}]}}`), 0o600))

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorFail, c.Status)
		joined := strings.Join(c.Details, "\n")
		assert.Contains(t, joined, tmplPath)
		assert.Contains(t, joined, fmt.Sprintf("template version %d", staleVersion))
		assert.Contains(t, joined, "re-download it")
	})

	t.Run("canary passes, referenced template file is missing -> fail naming the missing path", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, denyingCanaryStub)

		settingsDir := filepath.Join(root, ".claude")
		require.NoError(t, os.MkdirAll(settingsDir, 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(settingsDir, "settings.json"),
			[]byte(`{"hooks":{"PreToolUse":[{"hooks":[{"command":"sh docs/guides/integrations/agents/magus-guard-command.sh"}]}]}}`), 0o600))
		// No template file written at all: the config names one that does not exist.

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorFail, c.Status)
		joined := strings.Join(c.Details, "\n")
		assert.Contains(t, joined, "magus-guard-command.sh")
		assert.Contains(t, joined, "does not exist")
	})

	// The case measured on a real machine: an installed plugin predating the
	// marker, still calling a subcommand magus removed, graded healthy because
	// "no marker" was read as "nothing to compare". No marker means older than
	// versioning, which is the copy most likely to be judging nothing at all.
	t.Run("a template carrying no marker at all is a finding, not a pass", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, denyingCanaryStub)

		pluginsDir := filepath.Join(root, ".opencode", "plugins")
		require.NoError(t, os.MkdirAll(pluginsDir, 0o755))
		pluginPath := filepath.Join(pluginsDir, "magus-guard.ts")
		require.NoError(t, os.WriteFile(pluginPath,
			[]byte("// forwards to `magus agent hook`\nBun.spawn([magus, \"agent\", \"hook\", \"--\", command]);\n"), 0o644))

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorFail, c.Status)
		joined := strings.Join(c.Details, "\n")
		assert.Contains(t, joined, pluginPath)
		assert.Contains(t, joined, "predates template versioning")
	})

	t.Run("self-contained template discovered in a plugins directory checks its own marker", func(t *testing.T) {
		root := t.TempDir()
		writeGuardCanaryStub(t, root, denyingCanaryStub)

		pluginsDir := filepath.Join(root, ".opencode", "plugins")
		require.NoError(t, os.MkdirAll(pluginsDir, 0o755))
		staleVersion := agent.GuardTemplateVersion - 1
		pluginPath := filepath.Join(pluginsDir, "guard.ts")
		require.NoError(t, os.WriteFile(pluginPath,
			[]byte(fmt.Sprintf("// magus session hook\n// %s %d\n", agent.GuardTemplateMarker, staleVersion)), 0o644))

		c := checkGuardWiring(context.Background(), root, t.TempDir(), testCanaryBudget)
		require.Equal(t, types.DoctorFail, c.Status)
		joined := strings.Join(c.Details, "\n")
		assert.Contains(t, joined, pluginPath)
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
		assert.Equal(t, types.DoctorCheck{
			Name:    "language coverage",
			Status:  types.DoctorOK,
			Message: "every project matched a spell or declared no_language (1 exempt)",
		}, got)
	})

	t.Run("an undeclared gap is still reported", func(t *testing.T) {
		got := r.checkLanguageCoverage([]*types.Project{
			{Path: "evals", NoLanguage: "polyglot harness; no single pack describes it"},
			{Path: "forgot-the-import"},
		})
		assert.Equal(t, types.DoctorAdvice, got.Status)
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
// ctx.writesFiles - reading only p.Outputs would scan nothing here and report clean.
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
// state - a generator still writes the same hash into the same files once they are
// untracked - so the check has to skip instead of guess.
func TestSelfStalingSkipsWithoutTrackedReporter(t *testing.T) {
	// A non-git tree resolves no VCS, which is the same degrade path.
	r := &runner{root: t.TempDir(), ws: stubWorkspace{}}
	got := r.checkSelfStalingOutputs(nil)
	assert.Equal(t, types.DoctorOK, got.Status)
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

	require.Equal(t, types.DoctorAdvice, got.Status, got.Message)
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

	assert.NotEqual(t, types.DoctorFail, got.Status, "this check must never gate a build")
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

	assert.Equal(t, types.DoctorOK, got.Status, got.Message)
}

// TestUndeclaredSeedingFilesWithoutVCS degrades rather than guesses: with nothing to
// enumerate committed files with, the check has no candidate set at all, and a walk of
// the working tree would answer a different question (it would count build residue).
func TestUndeclaredSeedingFilesWithoutVCS(t *testing.T) {
	dir := t.TempDir() // not a repository
	r := &runner{root: dir, ws: stubWorkspace{}}

	got := r.checkUndeclaredSeedingFiles([]*types.Project{seedingProject()})

	assert.Equal(t, types.DoctorOK, got.Status, got.Message)
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

	require.Equal(t, types.DoctorFail, got.Status, got.Message)
	require.Len(t, got.Details, 1, "only the glob reaching into gen/ is unmatchable")
	assert.Contains(t, got.Details[0], "proto/gen/*.binpb")
	assert.Contains(t, got.Details[0], `prunes "gen"`)
	assert.Contains(t, got.Message, "MGS1029")
}

// TestUnmatchableSourceGlobsIgnoresExactPaths is the half that must not regress. A
// wildcard-free declaration names ONE file and is resolved by stat rather than the
// walk, so it reaches the cache key from inside a pruned tree normally - reporting it
// would tell the author to fix something that already works.
func TestUnmatchableSourceGlobsIgnoresExactPaths(t *testing.T) {
	r := &runner{root: t.TempDir(), ws: stubWorkspace{}}
	p := &types.Project{Path: "docs", Name: "docs", Dir: "docs", Sources: []string{
		"proto/gen/descriptor.binpb",
		"node_modules/pkg/index.js",
	}}

	got := r.checkUnmatchableSourceGlobs([]*types.Project{p})

	assert.Equal(t, types.DoctorOK, got.Status, got.Message)
	assert.Empty(t, got.Details)
}

// TestCheckAgentSkills pins what doctor DOES with the answer; grading an installed copy
// against the binary belongs to internal/agent and is tested there.
func TestCheckAgentSkills(t *testing.T) {
	t.Run("no catalog supplied -> skipped, not a finding", func(t *testing.T) {
		r := &runner{ws: rootStubWorkspace{root: t.TempDir()}}

		got := r.checkAgentSkills()

		assert.Equal(t, types.DoctorOK, got.Status)
		assert.Contains(t, got.Message, "skipped")
	})

	t.Run("nothing installed -> advice naming the install command", func(t *testing.T) {
		r := &runner{ws: rootStubWorkspace{root: t.TempDir()}}
		r.opts.skills = agent.NewCatalog(fstest.MapFS{}, "", 1)

		got := r.checkAgentSkills()

		require.Equal(t, types.DoctorAdvice, got.Status)
		assert.Contains(t, strings.Join(got.Details, "\n"), "magus agent install")
		assert.Empty(t, got.Fix, "install into WHICH directory is the developer's choice, so there is nothing to apply")
	})
}
