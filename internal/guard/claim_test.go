package guard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/types"
	"github.com/egladman/magus/types/agent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// IntegrationMergePathsSuite integrates a branch that changed merged.go.
type IntegrationMergePathsSuite struct {
	suite.Suite
	root string
	row  types.Job
}

func TestIntegrationMergePathsSuite(t *testing.T) {
	suite.Run(t, new(IntegrationMergePathsSuite))
}

func (s *IntegrationMergePathsSuite) git(args ...string) string {
	cmd := exec.Command("git", append([]string{"-C", s.root, "-c", "user.name=t", "-c", "user.email=t@t"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	s.Require().NoError(err, "%s", out)
	return strings.TrimSpace(string(out))
}

func (s *IntegrationMergePathsSuite) SetupTest() {
	s.root = s.T().TempDir()
	s.git("init", "-q", "-b", "landing")
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "merged.go"), []byte("package a\n"), 0o644))
	s.git("add", "merged.go")
	s.git("commit", "-q", "-m", "base")
	base := s.git("rev-parse", "HEAD")
	s.git("checkout", "-q", "-b", "job-a")
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "merged.go"), []byte("package a\nvar a = 1\n"), 0o644))
	s.git("commit", "-q", "-am", "a")
	s.git("checkout", "-q", "landing")
	s.row = claimLease("integration", "run.go")
	s.row.Checkpoint, s.row.Integrates = base, []string{"job-a", "job-gone"}
}

func (s *IntegrationMergePathsSuite) grade(rel string) (writeGrade, string) {
	return gradeClaimOutside(s.T().Context(), Dependencies{}, location{}, s.root, s.row, []types.Job{s.row}, nil, rel)
}

func (s *IntegrationMergePathsSuite) TestAPathABranchItIntegratesChangedIsInScope() {
	g, stop := s.grade("merged.go")
	s.Empty(stop)
	s.Equal(decisionAdvise, g.Decision)
	s.Contains(g.Context, "merged.go is in scope")
	s.Contains(g.Context, "job-a")
}

func (s *IntegrationMergePathsSuite) TestAPathNoBranchChangedStaysRefused() {
	g, stop := s.grade("other.go")
	s.Equal(writeGrade{}, g)
	s.Equal("integration integrates 2 branches, and none of them changed other.go", stop)
}

const claimedGo = `package run

func A() {
	a()
}

func B() {
	b()
}

func C() {
	c()
}
`

// claimFixture is a git checkout holding run.go under the golang driver, with a plan where
// own-a claims run.go#A and the writer, own-b, claims run.go#B.
func claimFixture(t *testing.T, extra ...types.Job) (context.Context, string) {
	t.Helper()
	leases := []types.Job{claimLease("own-a", "run.go#A"), claimLease("own-b", "run.go#B")}
	ctx, root := fleetFixture(t, append(leases, extra...)...)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.go diff=golang\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "run.go"), []byte(claimedGo), 0o644))
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	return ctx, root
}

func claimLease(id string, paths ...string) types.Job {
	return types.Job{
		ID: id, Agent: agent.Instructed("work on " + id), Paths: types.JobPaths{Write: paths}, State: types.StateRunning,
		Checkpoint: "rev", ReportedBase: "rev", BaseVerdict: types.BaseMatch, Registered: 1,
	}
}

// A write outside an integration row's paths is refused with no claim observed: reading the
// row's diff since its checkpoint took the whole judgment past its deadline.
func TestAnIntegrationRowObservesNoClaimFacts(t *testing.T) {
	row := claimLease("integration", "run.go")
	row.Integrates = []string{"job-a", "job-b"}

	g, stop := gradeClaimOutside(t.Context(), Dependencies{}, location{}, t.TempDir(), row, []types.Job{row}, nil, "other.go")

	assert.Equal(t, writeGrade{}, g)
	assert.Equal(t, "integration integrates 2 branches, and none of them changed other.go", stop)
}

func TestGradeClaimedDeclarations(t *testing.T) {
	ctx, root := claimFixture(t, claimLease("own-file", "other.go", "run.go"), narrowedLease("own-none", "run.go"))
	narrowSeeded(ctx, t, root, "own-none", "run.go")
	run := filepath.Join(root, "run.go")
	edit := func(oldText, newText string) writeFields { return writeFields{OldText: oldText, NewText: newText} }
	for _, tc := range []struct {
		name   string
		lease  string
		fields writeFields
		denied bool
	}{
		{name: "inside its own claim", lease: "own-b", fields: edit("b()", "b2()")},
		{name: "into another job's claim", lease: "own-b", fields: edit("a()", "a2()"), denied: true},
		{name: "into a declaration nobody claims", lease: "own-b", fields: edit("c()", "c2()")},
		{name: "above the first declaration", lease: "own-b", fields: edit("package run\n", "package run\n\nimport \"fmt\"\n")},
		{name: "a new function after its own", lease: "own-b", fields: edit("\tb()\n}\n", "\tb()\n}\n\nfunc B2() {\n}\n")},
		{name: "every edit of a sequence is placed", lease: "own-b", fields: writeFields{Edits: []textEdit{
			{OldText: "b()", NewText: "b2()"}, {OldText: "a()", NewText: "a2()"},
		}}, denied: true},
		{name: "a whole-file write inside its own claim", lease: "own-b", fields: writeFields{Content: strings.Replace(claimedGo, "b()", "b2()", 1)}},
		{name: "a whole-file write changing another job's declaration", lease: "own-b", fields: writeFields{Content: strings.Replace(claimedGo, "a()", "a2()", 1)}, denied: true},
		{name: "a whole-file write deleting another job's declaration", lease: "own-b", fields: writeFields{Content: "package run\n"}, denied: true},
		{name: "a whole-file write leaving the file as it is", lease: "own-b", fields: writeFields{Content: claimedGo}},
		{name: "an edit that does not apply keeps the path verdict", lease: "own-b", fields: edit("absent()", "x()")},
		{name: "an ambiguous edit keeps the path verdict", lease: "own-b", fields: edit("()", "(x)")},
		{name: "a writer holding the whole file", lease: "own-file", fields: edit("a()", "a2()")},
		{name: "a narrowed hold with no claim yet", lease: "own-none", fields: edit("a()", "a2()"), denied: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := gradeLeasedEdit(ctx, Dependencies{}, tc.lease, run, tc.fields)
			if !tc.denied {
				assert.Empty(t, got.Decision, got.Reason)
				return
			}
			// The reason is checked by substring below, so it is copied across.
			require.Equal(t, writeGrade{Decision: "deny", Reason: got.Reason, Rule: string(denyRuleClaimedDeclaration)}, got)
			assert.Contains(t, got.Reason, "Lease own-a (work on own-a) claims it as run.go#A")
			assert.Contains(t, got.Reason, "magus workspace: this edit changes func A() { in run.go, a declaration lease own-a claims;")
			assert.Contains(t, got.Reason, "`magus job exit own-a`")
		})
	}
}

// The real hook envelope, as Claude Code sends it for an edit, reaches the declaration rule
// through Judge.
func TestJudgeDeniesAnEditIntoAnotherJobsDeclaration(t *testing.T) {
	ctx, root := claimFixture(t)
	run := filepath.Join(root, "run.go")
	judge := func(toolInput map[string]any) types.GuardJudgment {
		return Judge(ctx, Dependencies{}, Request{Host: "claude-code", Lease: "own-b", Input: hookJSON(t, map[string]any{
			"session_id": "8f2c6a1e", "hook_event_name": "PreToolUse", "tool_name": "Edit", "cwd": root,
			"tool_input": toolInput,
		})})
	}

	v := judge(map[string]any{"file_path": run, "old_string": "a()", "new_string": "a2()"})
	require.Equal(t, verdictWithRule("deny", string(denyRuleClaimedDeclaration)), unworded(v))
	assert.Contains(t, storedVerdict(t, hookLocation(ctx, Dependencies{}).cacheDir, v.Reason), "claims it as run.go#A")

	multi := judge(map[string]any{"file_path": run, "edits": []any{
		map[string]any{"old_string": "b()", "new_string": "b2()"},
		map[string]any{"old_string": "a()", "new_string": "a2()", "replace_all": true},
	}})
	assert.Equal(t, "deny", multi.Decision, "every edit of a list is placed")

	assert.NotEqual(t, "deny", judge(map[string]any{"file_path": run, "old_string": "b()", "new_string": "b2()"}).Decision)
	assert.NotEqual(t, "deny", judge(map[string]any{"file_path": run, "content": strings.Replace(claimedGo, "b()", "b2()", 1)}).Decision,
		"a whole-file write that changes only the writer's declaration is placed like an edit and passes")
	assert.Equal(t, "deny", judge(map[string]any{"file_path": run, "content": "package run\n"}).Decision,
		"a whole-file write that drops another job's declaration is placed like an edit and denied")
}

const claimedMagusfile = `import "magus";

magus\project({
    "name": "demo",
    "targets": {
        "test": {"timeout": "5m"},
    },
});
`

// narrowedHoldSuite pins the holder's own write narrowing its hold and growing
// its claims, in a git checkout under the buzz and golang drivers.
type narrowedHoldSuite struct {
	suite.Suite
	ctx  func() context.Context // a func: containedctx flags a struct holding a Context
	root string
}

func TestNarrowedHold(t *testing.T) { suite.Run(t, new(narrowedHoldSuite)) }

// narrowedLease holds file by name and claims decls. The store drops a
// release a new row carries, so narrowSeeded records it later.
func narrowedLease(id, file string, decls ...string) types.Job {
	row := claimLease(id, file)
	for _, d := range decls {
		row.Paths.Write = append(row.Paths.Write, fmt.Sprintf("%s#%s", file, d))
	}
	return row
}

func narrowSeeded(ctx context.Context, t *testing.T, root, id, file string) {
	t.Helper()
	loc := hookLocation(ctx, Dependencies{}).jobs()
	loc.Root = root
	_, _, err := job.NarrowHold(ctx, job.NewStore(loc), id, file, nil, "")
	require.NoError(t, err)
}

func (s *narrowedHoldSuite) start(leases ...types.Job) {
	ctx, root := fleetFixture(s.T(), leases...)
	s.ctx, s.root = func() context.Context { return ctx }, root
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, ".gitattributes"), []byte("*.buzz diff=buzz\n*.go diff=golang\n"), 0o644))
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "magusfile.buzz"), []byte(claimedMagusfile), 0o644))
	s.Require().NoError(os.WriteFile(filepath.Join(s.root, "run.go"), []byte(claimedGo), 0o644))
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = s.root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	s.Require().NoError(err, "%s", out)
}

func (s *narrowedHoldSuite) row(id string) types.Job {
	rows, err := leaseRows(s.ctx(), hookLocation(s.ctx(), Dependencies{}))
	s.Require().NoError(err)
	return rows[slices.IndexFunc(rows, func(r types.Job) bool { return r.ID == id })]
}

func (s *narrowedHoldSuite) write(lease, file string, fields writeFields) writeGrade {
	return gradeLeasedEdit(s.ctx(), Dependencies{}, lease, filepath.Join(s.root, file), fields)
}

// A lease holding every Buzz file by a pattern narrows its hold on the magusfile to the keys
// its first write changes, releasing the rest with its digest; from then on its claims are
// graded like any other, in both directions.
func (s *narrowedHoldSuite) TestAPatternHoldNarrowsOnFirstWrite() {
	s.start(claimLease("cfg", "**/*.buzz"), claimLease("keys", "magusfile.buzz#project.name"))
	timeout := writeFields{OldText: `"5m"`, NewText: `"6m"`}
	name := writeFields{OldText: `"demo"`, NewText: `"demo2"`}

	s.Empty(s.write("keys", "magusfile.buzz", timeout).Decision,
		"before the holder writes, its pattern claims no key another lease could collide with")

	got := s.write("cfg", "magusfile.buzz", timeout)
	s.Require().Equal("advise", got.Decision, got.Reason)
	s.Contains(got.Context, `lease cfg now holds magusfile.buzz by "magusfile.buzz#project.targets.test"`)
	cfg := s.row("cfg")
	s.Equal([]string{"**/*.buzz", "magusfile.buzz#project.targets.test"}, cfg.Paths.Write)
	s.Require().Len(cfg.Releases, 1)
	s.Equal("magusfile.buzz", cfg.Releases[0].Path)
	s.True(cfg.Releases[0].Narrowed)
	s.True(strings.HasPrefix(cfg.Releases[0].Digest, "sha256:"), cfg.Releases[0].Digest)

	s.Empty(s.write("cfg", "magusfile.buzz", timeout).Decision, "a key it claims narrows nothing more")
	s.Equal("deny", s.write("cfg", "magusfile.buzz", name).Decision, "a key another lease claims")
	s.Equal("deny", s.write("keys", "magusfile.buzz", timeout).Decision, "the holder's new claim is graded")
	s.Empty(s.write("keys", "magusfile.buzz", name).Decision)
	s.Len(s.row("cfg").Paths.Write, 2, "a denied write narrows nothing")
}

func (s *narrowedHoldSuite) TestAPatternHoldOnAnyPlacedFileNarrows() {
	s.start(claimLease("code", "*.go"))

	got := s.write("code", "run.go", writeFields{OldText: "c()", NewText: "c2()"})
	s.Require().Equal("advise", got.Decision, got.Reason)
	s.Contains(got.Context, `lease code now holds run.go by "run.go#C"`)
	s.Equal([]string{"*.go", "run.go#C"}, s.row("code").Paths.Write)
}

func (s *narrowedHoldSuite) TestANarrowedLiteralEntryGrowsOnWrite() {
	s.start(narrowedLease("lit", "run.go", "B"), claimLease("own-a", "run.go#A"))
	narrowSeeded(s.ctx(), s.T(), s.root, "lit", "run.go")

	got := s.write("lit", "run.go", writeFields{OldText: "c()", NewText: "c2()"})
	s.Require().Equal("advise", got.Decision, got.Reason)
	s.Contains(got.Context, `lease lit's hold on run.go grew by "run.go#C"; it now holds the file by "run.go#B", "run.go#C"`)
	s.Equal([]string{"run.go", "run.go#B", "run.go#C"}, s.row("lit").Paths.Write, "the entry stays")

	s.Empty(s.write("lit", "run.go", writeFields{OldText: "b()", NewText: "b2()"}).Decision, "a write inside its claims grows nothing")
	s.Equal("deny", s.write("lit", "run.go", writeFields{OldText: "a()", NewText: "a2()"}).Decision, "a declaration another lease claims")
	s.Len(s.row("lit").Paths.Write, 3, "a denied write grows nothing")
}

func (s *narrowedHoldSuite) TestANarrowedHoldWithNoClaimYetIsDeniedAnotherJobsDeclaration() {
	s.start(narrowedLease("lit", "run.go"), claimLease("own-a", "run.go#A"))
	narrowSeeded(s.ctx(), s.T(), s.root, "lit", "run.go")

	got := s.write("lit", "run.go", writeFields{OldText: "a()", NewText: "a2()"})
	s.Equal(writeGrade{Decision: "deny", Rule: string(denyRuleClaimedDeclaration)},
		writeGrade{Decision: got.Decision, Rule: got.Rule}, got.Reason)
}

func (s *narrowedHoldSuite) TestALiteralEntryNobodyNarrowedStaysWhole() {
	s.start(claimLease("lit", "run.go"))

	s.Empty(s.write("lit", "run.go", writeFields{OldText: "c()", NewText: "c2()"}).Decision)
	s.Equal([]string{"run.go"}, s.row("lit").Paths.Write)
}

const siblingKeysMagusfile = `import "magus";

magus\project({
    "name": "demo",
    "targets": {
        "release": {"timeout": "1m"},
        "release-build": {"timeout": "2m"},
    },
});
`

// A claim on a target key names that key, not a sibling that shares its prefix: the lease
// holding `release` may not write `release-build`, which another lease claims.
func TestKeyClaimNamesItsOwnKey(t *testing.T) {
	ctx, root := fleetFixture(t,
		claimLease("rel", "magusfile.buzz#project.targets.release"),
		claimLease("build", "magusfile.buzz#project.targets.release-build"),
	)
	require.NoError(t, os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("*.buzz diff=buzz\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(root, "magusfile.buzz"), []byte(siblingKeysMagusfile), 0o644))
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s", out)
	magusfile := filepath.Join(root, "magusfile.buzz")
	release := writeFields{OldText: `"1m"`, NewText: `"3m"`}
	build := writeFields{OldText: `"2m"`, NewText: `"4m"`}

	for _, tc := range []struct {
		name   string
		lease  string
		fields writeFields
		want   string
	}{
		{"release writes its own key", "rel", release, ""},
		{"release-build writes its own key", "build", build, ""},
		{"release writes the sibling's key", "rel", build, "deny"},
		{"release-build writes the sibling's key", "build", release, "deny"},
	} {
		assert.Equal(t, tc.want, gradeLeasedEdit(ctx, Dependencies{}, tc.lease, magusfile, tc.fields).Decision, tc.name)
	}
}

func TestApplyEdits(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		fields writeFields
		want   string
		ok     bool
	}{
		{"one replacement", writeFields{OldText: "b", NewText: "B"}, "aBca", true},
		{"no edit at all", writeFields{Content: "x"}, "", false},
		{"absent text", writeFields{OldText: "z", NewText: "Z"}, "", false},
		{"ambiguous text", writeFields{OldText: "a", NewText: "A"}, "", false},
		{"ambiguous text replaced everywhere", writeFields{OldText: "a", NewText: "A", ReplaceAll: true}, "AbcA", true},
		{"a sequence, each on the result of the last", writeFields{Edits: []textEdit{{OldText: "b", NewText: "bb"}, {OldText: "bb", NewText: "X"}}}, "aXca", true},
		{"a sequence with one that fails", writeFields{Edits: []textEdit{{OldText: "b", NewText: "B"}, {OldText: "z", NewText: "Z"}}}, "", false},
	} {
		got, ok := applyEdits("abca", tc.fields)
		assert.Equal(t, tc.ok, ok, tc.name)
		if tc.ok {
			assert.Equal(t, tc.want, got, tc.name)
		}
	}
}
