package guard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egladman/magus/internal/gen/typestest"
	"github.com/egladman/magus/internal/guard/builtin"
	"github.com/egladman/magus/internal/hint"
	"github.com/egladman/magus/internal/job"
	"github.com/egladman/magus/libs/clikit/output"
	jobtypes "github.com/egladman/magus/libs/safetyscissors/job"
	"github.com/egladman/magus/libs/testkit"
	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// commitGradeRoutesSuite pins the routes a commit took around its path grade.
type commitGradeRoutesSuite struct {
	commitPathsSuite
}

func TestCommitGradeRoutes(t *testing.T) { suite.Run(t, new(commitGradeRoutesSuite)) }

func (s *commitGradeRoutesSuite) script(name, body string) string {
	p := filepath.Join(s.dir, name)
	s.Require().NoError(os.WriteFile(p, []byte(body), 0o755))
	return p
}

func (s *commitGradeRoutesSuite) TestAPickOutsideTheWritePathsIsRefusedAndNamesTheRoute() {
	picked := s.repo.commitFiles(s.repo.main, map[string]string{"gen.json": "x\n"})
	got := s.deny("git cherry-pick " + picked)
	s.Contains(got, "this commit carries paths outside lease")
	s.Contains(got, "It picks commits writing gen.json")
	s.Contains(got, "git checkout <commit> -- <path>")
}

func (s *commitGradeRoutesSuite) TestAPickThroughTheVCSCommandIsGraded() {
	picked := s.repo.commitFiles(s.repo.main, map[string]string{"gen.json": "x\n"})
	err := vcsCommandRefusal(s.testContext(), s.worker, "git", []string{"cherry-pick", picked}, s.dir, nil)
	s.Require().Error(err)
	s.Contains(err.Error(), "It picks commits writing gen.json")
}

func (s *commitGradeRoutesSuite) TestAPickInsideTheWritePathsPasses() {
	s.Require().NoError(os.MkdirAll(filepath.Join(s.repo.main, "cmd/magus"), 0o755))
	picked := s.repo.commitFiles(s.repo.main, map[string]string{"cmd/magus/x.go": "x\n"})
	s.Empty(s.deny("git cherry-pick " + picked))
}

func (s *commitGradeRoutesSuite) TestAPickFromStandardInputIsRefusedThroughTheVCSCommand() {
	err := vcsCommandRefusal(s.testContext(), s.worker, "git", []string{"cherry-pick", "--stdin"}, s.dir, nil)
	s.Require().Error(err)
	s.Contains(err.Error(), "Name each commit in the call")
}

func (s *commitGradeRoutesSuite) TestContinuingAPickPasses() {
	s.Empty(s.deny("git cherry-pick --continue"))
}

func (s *commitGradeRoutesSuite) TestAnIntegrationRowPicksWhatItsWritePathsCover() {
	picked := s.repo.commitFiles(s.repo.main, map[string]string{"gen.json": "x\n"})
	row := s.worker
	row.Paths.Write = append(row.Paths.Write, "gen.json")
	row.Integrates = []string{"other"}
	ctx, _ := fleetFixture(s.T(), row)
	s.Empty(denyLeaseScopedVCS(ctx, Dependencies{callDir: s.dir}, row.ID, "git cherry-pick "+picked))
}

func (s *commitGradeRoutesSuite) TestAPipedPathspecBesideACommitNamesTheTwoCalls() {
	got := s.deny("printf 'docs/gen.json\\n' | git add --pathspec-from-file=- && git commit -m done")
	s.Contains(got, "cannot be read before the line runs")
	s.Contains(got, "then commit in the next")
}

func (s *commitGradeRoutesSuite) TestThePipedRemedyAloneStillStages() {
	s.Empty(s.deny("magus vcs ls-files -o name | git add --pathspec-from-file=-"))
}

func (s *commitGradeRoutesSuite) TestAPathspecFileOnDiskIsRead() {
	s.write("docs/gen.json")
	s.script("list", "docs/gen.json\n")
	s.Contains(s.deny("git add --pathspec-from-file=list && git commit -m done"), "It commits docs/gen.json")
}

func (s *commitGradeRoutesSuite) TestAPathspecFileTheLineWritesIsRefused() {
	s.Contains(s.deny("git add --pathspec-from-file=later && git commit -m done"), "then commit in the next")
}

func (s *commitGradeRoutesSuite) TestABranchForceTypedIsRefused() {
	s.Contains(s.deny(fmt.Sprintf("git branch -f %s HEAD~1", s.worker.ID)), "`git branch` stays with the orchestrator")
}

func (s *commitGradeRoutesSuite) TestAScriptHidingABranchForceIsRefused() {
	script := s.script("fold.sh", fmt.Sprintf("#!/bin/bash\ngit branch -f %s HEAD\n", s.worker.ID))
	for _, line := range []string{"bash " + script, "sh " + script, script, "bash fold.sh"} {
		got := s.deny(line)
		s.Contains(got, "`git branch` stays with the orchestrator", line)
		s.Contains(got, "a script gets the verdict its lines would get typed", line)
	}
}

func (s *commitGradeRoutesSuite) TestAScriptHidingARebaseIsRefused() {
	script := s.script("fold.sh", "git rebase main\n")
	s.Contains(s.deny("bash "+script), "a worker rebases only its own job's branch")
}

func (s *commitGradeRoutesSuite) TestAScriptTheWorkerMayRunPasses() {
	script := s.script("ok.sh", "git status --short\ngit log -1\n")
	s.Empty(s.deny("bash " + script))
}

func (s *commitGradeRoutesSuite) TestAScriptNotOnDiskIsRefusedWithTheRoute() {
	s.Contains(s.deny("bash missing.sh"), "Write the script first, then run it in a call of its own")
}

func (s *commitGradeRoutesSuite) TestAProgramThatIsNotAShellScriptIsLeftAlone() {
	s.Empty(s.deny(s.script("tool", "\x7fELF")))
}

func (s *commitGradeRoutesSuite) TestAScriptCallingItselfIsRefusedAtTheBound() {
	script := filepath.Join(s.dir, "self.sh")
	s.script("self.sh", fmt.Sprintf("sh %s\n", script))
	s.Contains(s.deny("sh "+script), "nest deeper than")
}

func (s *commitGradeRoutesSuite) TestTwoScriptsCallingEachOtherAreRefusedAtTheBound() {
	first, second := filepath.Join(s.dir, "first.sh"), filepath.Join(s.dir, "second.sh")
	s.script("first.sh", fmt.Sprintf("sh %s\n", second))
	s.script("second.sh", fmt.Sprintf("sh %s\n", first))
	s.Contains(s.deny("sh "+first), "nest deeper than")
}

func (s *commitGradeRoutesSuite) TestAScriptCallingItselfTwiceUnderADeadLeasePasses() {
	script := filepath.Join(s.dir, "self.sh")
	s.script("self.sh", fmt.Sprintf("sh %s\nsh %s\n", script, script))
	s.Empty(denyLeaseScopedVCS(s.testContext(), Dependencies{callDir: s.dir}, "no-such-lease", "sh "+script))
}

func (s *commitGradeRoutesSuite) TestAVerbInANestedScriptIsRefused() {
	inner := s.script("inner.sh", "git rebase main\n")
	outer := s.script("outer.sh", fmt.Sprintf("sh %s\n", inner))
	s.Contains(s.deny("sh "+outer), "a worker rebases only its own job's branch")
}

// scriptIn writes an executable script in a subdirectory of the checkout.
func (s *commitGradeRoutesSuite) scriptIn(dir, name, body string) {
	s.Require().NoError(os.MkdirAll(filepath.Join(s.dir, dir), 0o755))
	s.script(filepath.Join(dir, name), body)
}

func (s *commitGradeRoutesSuite) TestACdBeforeAScriptFindsItThere() {
	s.scriptIn("tools", "ok.sh", "git status --short\n")
	s.Empty(s.deny("cd tools && ./ok.sh"))
}

func (s *commitGradeRoutesSuite) TestACdBeforeAScriptJudgesTheOneItRuns() {
	s.script("run.sh", "git status --short\n")
	s.scriptIn("tools", "run.sh", "git rebase main\n")
	s.Contains(s.deny("cd tools && ./run.sh"), "a worker rebases only its own job's branch")
	s.Empty(s.deny("./run.sh"))
}

func (s *commitGradeRoutesSuite) TestAScriptRunsItsOwnRelativeCallsFromTheCd() {
	s.scriptIn("tools", "outer.sh", "./inner.sh\n")
	s.scriptIn("tools", "inner.sh", "git rebase main\n")
	s.Contains(s.deny("cd tools && ./outer.sh"), "a worker rebases only its own job's branch")
}

func (s *commitGradeRoutesSuite) TestAScriptNameIsJudgedInEachDirectoryItRuns() {
	s.script("run.sh", "git status --short\n")
	s.scriptIn("tools", "run.sh", "git rebase main\n")
	s.Contains(s.deny("./run.sh && cd tools && ./run.sh"), "a worker rebases only its own job's branch")
}

func (s *commitGradeRoutesSuite) TestARelativeScriptAfterAnUnknownCdIsRefused() {
	s.script("ok.sh", "git status --short\n")
	s.Contains(s.deny(`cd "$WHERE" && ./ok.sh`), "from a directory only running the line can reveal")
}

func (s *commitGradeRoutesSuite) TestAnAbsoluteScriptAfterAnUnknownCdIsRead() {
	script := s.script("ok.sh", "git status --short\n")
	s.Empty(s.deny(fmt.Sprintf(`cd "$WHERE" && %s`, script)))
}

func (s *commitGradeRoutesSuite) TestARelativeCallInAScriptAfterAnUnknownCdIsRefused() {
	s.scriptIn("tools", "inner.sh", "git rebase main\n")
	script := s.script("outer.sh", "./inner.sh\n")
	s.Contains(s.deny(fmt.Sprintf(`cd "$WHERE" && %s`, script)), "from a directory only running the line can reveal")
}

func (s *commitGradeRoutesSuite) TestACommitInAScriptAfterAnUnknownCdIsRefused() {
	script := s.script("save.sh", "git commit -m done\n")
	s.Contains(s.deny(fmt.Sprintf(`cd "$WHERE" && %s`, script)), "where it commits cannot be read")
}

func (s *commitGradeRoutesSuite) TestALiteralCdInAScriptAfterAnUnknownCdIsRead() {
	s.scriptIn("tools", "inner.sh", "git rebase main\n")
	script := s.script("outer.sh", fmt.Sprintf("cd %s && ./inner.sh\n", filepath.Join(s.dir, "tools")))
	s.Contains(s.deny(fmt.Sprintf(`cd "$WHERE" && %s`, script)), "a worker rebases only its own job's branch")
}

func (s *commitGradeRoutesSuite) TestAScriptInASubstitutionRunsFromTheCallDirectory() {
	s.script("ok.sh", "git status --short\n")
	s.Empty(s.deny("x=$(./ok.sh)"))
}

func (s *commitGradeRoutesSuite) TestAScriptInASubstitutionAfterACdIsRefused() {
	s.scriptIn("tools", "ok.sh", "git status --short\n")
	s.Contains(s.deny("cd tools && x=$(./ok.sh)"), "from a directory only running the line can reveal")
}

func (s *commitGradeRoutesSuite) TestATwoWordPathspecFileCommitIsGraded() {
	s.write("a.txt")
	s.script("list.txt", "a.txt\n")
	s.Contains(s.deny("git commit --pathspec-from-file list.txt -m done"), "It commits a.txt")
}

func (s *commitGradeRoutesSuite) TestAPathspecFileWithEqualsCommitIsGraded() {
	s.write("a.txt")
	s.script("list.txt", "a.txt\n")
	s.Contains(s.deny("git commit --pathspec-from-file=list.txt -m done"), "It commits a.txt")
}

// integrating commits three paths no write path holds, as an integrating row.
func (s *commitGradeRoutesSuite) integrating(inspect func(context.Context, string) (types.WorkspaceRepository, error)) string {
	s.write("docs/one.json")
	s.write("docs/two.json")
	s.write("docs/three.json")
	s.repo.git(s.dir, "add", "docs")
	row := s.worker
	row.Integrates = []string{"other"}
	ctx, _ := fleetFixture(s.T(), row)
	return denyLeaseScopedVCS(ctx, Dependencies{callDir: s.dir, Inspect: inspect}, row.ID, "git commit -m done")
}

func (s *commitGradeRoutesSuite) TestAnIntegrationCommitOpensTheWorkspaceOnce() {
	opens := 0
	project := &types.Project{Path: ".", TargetOutputs: map[string][]types.OutputRef{"gen": {{Glob: "docs/*.json"}}}}
	ws := typestest.NewMockWorkspaceRepository(s.T())
	ws.EXPECT().All().Return([]*types.Project{project}).Maybe()
	s.Empty(s.integrating(func(context.Context, string) (types.WorkspaceRepository, error) {
		opens++
		return ws, nil
	}))
	s.Equal(1, opens)
}

func (s *commitGradeRoutesSuite) TestAnIntegrationCommitThatCannotOpenTheWorkspaceSaysSo() {
	got := s.integrating(func(context.Context, string) (types.WorkspaceRepository, error) {
		return nil, errors.New("the magusfile raised")
	})
	s.Contains(got, "which of them a target regenerates cannot be read: the magusfile raised")
	s.NotContains(got, "It commits")
}

func TestMergesBase(t *testing.T) {
	row := jobtypes.Job{DependsOn: []string{"worker-a", "worker-b"}}
	for _, tc := range []struct {
		name    string
		operand string
		base    string
		want    bool
	}{
		{"the base", "main", "main", true},
		{"the base's origin form", "origin/main", "refs/heads/main", true},
		{"a depends_on branch", "worker-a", "main", true},
		{"a depends_on branch's origin form", "origin/worker-b", "main", true},
		{"a depends_on branch with no base resolved", "worker-a", "", true},
		{"an unrelated branch", "worker-c", "main", false},
		{"a prefix of a depends_on branch", "worker", "main", false},
		{"another remote's depends_on branch", "upstream/worker-a", "main", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, mergesBase(tc.operand, tc.base, row))
		})
	}

	t.Run("a row with no depends_on refuses a job branch", func(t *testing.T) {
		assert.False(t, mergesBase("worker-a", "main", jobtypes.Job{}))
	})
}

func TestWorkerVCSOp(t *testing.T) {
	op := func(t *testing.T, line string) string {
		t.Helper()
		cmds, ok := ParseCommands(line)
		require.True(t, ok, line)
		require.Len(t, cmds, 1, line)
		return workerVCSOp(cmds[0])
	}

	t.Run("reads and own-branch work pass", func(t *testing.T) {
		for _, line := range []string{
			"git status --short", "git -C ../x log --oneline", "git diff --stat", "git add a.go",
			"git branch", "git branch -vv", "git branch feature", "git switch -c feature",
			"git switch --detach main", "git checkout -b feature", "git checkout -- go.mod",
			"git restore --staged a.go", "git fetch origin", "git fetch https://h/r main",
			"git config --get user.name", "git config get user.name", "git tag", "git tag -l v*",
			"git remote -v", "git reflog -n 5", "git symbolic-ref --short HEAD",
			"git worktree list", "git apply --check p.diff", "git stash list", "git",
			"hg status", "hg bookmarks", "hg update tip", "sl smartlog",
			"jj log", "jj new", "jj bookmark list", "jj git fetch", "jj restore a.go",
			// A remove or move of named paths: lease-write grades each one.
			"git rm a.go", "git rm -r --cached internal/x", "git mv a.go b.go", "git rm -- -odd.go",
			"hg remove a.go", "hg rm -A a.go", "sl rename a.go b.go", "hg mv a.go dir/",
		} {
			assert.Empty(t, op(t, line), line)
		}
	})

	t.Run("every other verb is refused by name", func(t *testing.T) {
		for line, want := range map[string]string{
			"git branch -f main HEAD~1":           "git branch",
			"git branch -D other":                 "git branch",
			"git branch --set-upstream-to=o/main": "git branch",
			"git update-ref refs/heads/main HEAD": "git update-ref",
			"git switch -C main":                  "git switch",
			"git switch --discard-changes main":   "git switch",
			"git checkout -f main":                "git checkout",
			"git checkout -B main":                "git checkout",
			"git fetch origin main:main":          "git fetch",
			"git pull":                            "git pull",
			"git am p.mbox":                       "git am",
			"git apply p.diff":                    "git apply",
			"git config user.name x":              "git config",
			"git tag -d v1":                       "git tag",
			"git tag v1":                          "git tag",
			"git reflog expire --all":             "git reflog",
			"git symbolic-ref HEAD refs/heads/x":  "git symbolic-ref",
			"git worktree prune":                  "git worktree",
			"git gc --prune=now":                  "git gc",
			"git bisect start":                    "git bisect",
			"git myalias":                         "git myalias",
			"git reset --hard":                    "git reset",
			"git reset --hard f1980cc5c":          "git reset",
			"git merge f1980cc5c":                 "git merge",
			"git merge --no-ff f1980cc5c":         "git merge",
			"git merge --ff-only f1980cc5c":       "git merge",
			"git merge --ff-only main":            "git merge",
			"git merge --ff-only F1980CC5C":       "git merge",
			"git merge --ff-only f1980c":          "git merge",
			"git merge --ff-only a1b2c3d e4f5a6b": "git merge",
			"git merge --squash a1b2c3d":          "git merge",
			"git -C ../x merge --ff-only a1b2c3d": "git merge",
			"git pull --ff-only":                  "git pull",
			"hg strip tip":                        "hg strip",
			"hg update -C tip":                    "hg update",
			"hg bookmark -d other":                "hg bookmark",
			"hg pull":                             "hg pull",
			"sl hide .":                           "sl hide",
			"jj abandon":                          "jj abandon",
			"jj rebase -d main":                   "jj rebase",
			"jj squash":                           "jj squash",
			"jj edit other":                       "jj edit",
			"jj bookmark set main":                "jj bookmark",
			"jj op restore abc":                   "jj op",
			"jj restore":                          "jj restore",
		} {
			assert.Equal(t, want, op(t, line), line)
		}
	})

	// lease-write grades the words on the line, so a remove or move whose paths the VCS
	// expands would reach files no word names.
	t.Run("a remove or move that does not spell out its paths is refused", func(t *testing.T) {
		for line, want := range map[string]string{
			"git rm":                            "git rm",
			"git rm -r .":                       "git rm",
			"git rm -r ./":                      "git rm",
			"git rm '*.go'":                     "git rm",
			"git rm 'internal/[a-z]*'":          "git rm",
			"git rm ':(glob)**/*.go'":           "git rm",
			"git rm :/":                         "git rm",
			"git rm ../sibling/a.go":            "git rm",
			"git rm --pathspec-from-file=paths": "git rm",
			"git mv a.go ..":                    "git mv",
			"hg remove":                         "hg remove",
			"hg remove 'glob:**/*.go'":          "hg remove",
			"hg rm -I 'glob:**' a.go":           "hg rm",
			"sl rename --exclude x a.go b.go":   "sl rename",
		} {
			assert.Equal(t, want, op(t, line), line)
		}
	})

	t.Run("anything else is not version control", func(t *testing.T) {
		assert.Empty(t, op(t, "rm -rf build"))
	})
}

func TestIntegrationMergeAllowsOnlyTheListedBranches(t *testing.T) {
	row := jobtypes.Job{ID: "integrate", Integrates: []string{"done-a", "done-b"}}
	merge := func(line string, row jobtypes.Job) bool {
		cmds, ok := ParseCommands(line)
		require.True(t, ok, line)
		require.Len(t, cmds, 1, line)
		return integrationMerge(cmds[0], row)
	}
	for _, line := range []string{"git merge --no-ff done-a", "git merge done-b --no-ff", "git merge --no-ff --no-edit done-a"} {
		assert.True(t, merge(line, row), line)
	}
	for _, line := range []string{
		// A fast-forward hides that the branch was integrated; an unlisted branch, an octopus
		// merge, a strategy option, a squash, a relocation or another verb widens what runs.
		"git merge --no-ff other", "git merge --no-ff done-a done-b",
		"git merge --no-ff -X theirs done-a", "git merge --no-ff --squash done-a",
		"git -C ../x merge --no-ff done-a", "git rebase --no-ff done-a", "hg merge --no-ff done-a",
	} {
		assert.False(t, merge(line, row), line)
	}
	assert.False(t, merge("git merge --no-ff done-a", jobtypes.Job{ID: "worker"}), "a row integrating nothing merges nothing")
}

// A landing row commits and fast-forwards its own checkout, with or without -C.
// Force, added history, a branch name and any other checkout stay refused.
func TestLandingRowMovesItsCheckout(t *testing.T) {
	r := newWorktreeRepository(t)
	wt := r.add("lease-job")
	worker := narrowLease()
	worker.Parent = "orchestrator"
	worker.CheckoutRoot = wt
	ctx, _ := fleetFixture(t, worker)
	from := func(dir string) Dependencies {
		return Dependencies{callDir: dir, VCS: types.VCSOptions{BaseRef: "origin/main"}}
	}
	const rev = "4cf8063f2"

	for _, command := range []string{
		"git merge --ff-only " + rev,
		"git merge " + rev + " --ff-only",
		"git commit -m done",
	} {
		assert.Empty(t, denyLeaseScopedVCS(ctx, from(wt), worker.ID, command), "in place: %q", command)
	}
	for _, command := range []string{
		fmt.Sprintf("git -C %s merge --ff-only %s", wt, rev),
		fmt.Sprintf("git -C %s commit -m done", wt),
		fmt.Sprintf("cd %s && git merge --ff-only %s", wt, rev),
	} {
		assert.Empty(t, denyLeaseScopedVCS(ctx, from(r.main), worker.ID, command), "pointed at its own checkout: %q", command)
	}

	elsewhere := fmt.Sprintf("It commits in %s, and the lease was taken in %s.", r.main, wt)
	assert.Contains(t, denyLeaseScopedVCS(ctx, from(r.main), worker.ID, "git merge --ff-only "+rev), elsewhere)
	assert.Contains(t, denyLeaseScopedVCS(ctx, from(wt), worker.ID, fmt.Sprintf("git -C %s merge --ff-only %s", r.main, rev)), elsewhere)
	assert.Contains(t, denyLeaseScopedVCS(ctx, from(wt), worker.ID, fmt.Sprintf("cd %s && git merge --ff-only %s", r.main, rev)), elsewhere)

	onBase := Dependencies{callDir: wt, VCS: types.VCSOptions{BaseRef: "origin/lease-job"}}
	assert.Contains(t, denyLeaseScopedVCS(ctx, onBase, worker.ID, "git merge --ff-only "+rev), "the base branch")

	for _, command := range []string{
		fmt.Sprintf("git -C %s merge --ff-only other-job", wt),
		fmt.Sprintf("git -C %s merge %s", wt, rev),
		fmt.Sprintf("git -C %s merge --no-ff %s", wt, rev),
		fmt.Sprintf("git -C %s merge --ff-only --no-ff %s", wt, rev),
		fmt.Sprintf("git -C %s merge --ff-only --squash %s", wt, rev),
		fmt.Sprintf("git -C %s merge --ff-only -s ours %s", wt, rev),
		fmt.Sprintf("git -C %s merge --ff-only %s a1b2c3d", wt, rev),
		fmt.Sprintf("git -C %s reset --hard %s", wt, rev),
		fmt.Sprintf("git -C %s branch -f lease-job %s", wt, rev),
		fmt.Sprintf("git -C %s update-ref refs/heads/lease-job %s", wt, rev),
		fmt.Sprintf("git -C %s --work-tree=../x merge --ff-only %s", wt, rev),
	} {
		assert.NotEmpty(t, denyLeaseScopedVCS(ctx, from(r.main), worker.ID, command), "%q", command)
	}

	refused := denyLeaseScopedVCS(ctx, from(wt), worker.ID, "git merge a1b2c3d")
	assert.Contains(t, refused, "`git merge --ff-only <commit id>`", "the refusal names the fast-forward that passes")
}

func TestWorkerVCSMovesRefs(t *testing.T) {
	for line, want := range map[string]bool{
		"git switch -c topic":        true,
		"git switch main":            true,
		"git -C ../x checkout topic": true,
		"git checkout -- a.go":       true,
		"git branch topic":           true,
		"git branch -f topic HEAD~1": true,
		"git restore a.go":           true,
		"git stash":                  true,
		"git add -A":                 true,
		"git rm a.go":                true,
		"git mv a.go b.go":           true,
		"hg update tip":              true,
		"hg bookmark topic":          true,
		"hg add a.go":                true,
		"sl goto main":               true,
		"jj new main":                true,
		"jj edit abc":                true,
		"jj bookmark create topic":   true,
		"git branch":                 false,
		"git branch --list 'job-*'":  false,
		"git branch -a":              false,
		"git branch --show-current":  false,
		"git status":                 false,
		"git log --oneline":          false,
		"git diff --cached":          false,
		"git commit -m done":         false,
		"git merge --no-ff topic":    false,
		"hg bookmarks":               false,
		"hg log":                     false,
		"jj log":                     false,
		"jj bookmark list":           false,
		"ls -la":                     false,
	} {
		cmds, ok := ParseCommands(line)
		require.True(t, ok, line)
		require.Len(t, cmds, 1, line)
		assert.Equal(t, want, workerVCSMovesRefs(cmds[0]), line)
	}
}

func TestIntegrationMergeInALoop(t *testing.T) {
	worker := narrowLease()
	worker.Parent = "harness"
	worker.Integrates = []string{"done-a", "done-b"}
	ctx, _ := fleetFixture(t, worker)

	for _, command := range []string{
		"git merge --no-ff --no-edit done-a",
		`for b in done-a done-b; do git merge --no-ff --no-edit "$b"; done`,
		`for b in done-b; do git merge --no-ff --no-edit $b; done`,
	} {
		assert.Empty(t, denyLeaseScopedVCS(ctx, Dependencies{}, worker.ID, command), "%q", command)
	}

	for _, command := range []string{
		`for b in done-a other; do git merge --no-ff --no-edit "$b"; done`,
		`for b in $(git branch --list 'done-*'); do git merge --no-ff --no-edit "$b"; done`,
		`for b in done-*; do git merge --no-ff --no-edit "$b"; done`,
		`git merge --no-ff --no-edit "$b"`,
	} {
		assert.NotEmpty(t, denyLeaseScopedVCS(ctx, Dependencies{}, worker.ID, command), "%q", command)
	}
}

func TestMergeRefusalNamesTheBranchesARowIntegrates(t *testing.T) {
	row := jobtypes.Job{ID: "integrate", Integrates: []string{"done-a", "done-b"}}
	assert.Contains(t, mergeRefusal("734253d9a", "main", row), "or a branch it integrates, merged as <b>, origin/<b>, refs/heads/<b> or refs/remotes/origin/<b>: done-a, done-b.")
}

func TestMergeRefusalOfARowIntegratingNothingNamesNoBranch(t *testing.T) {
	assert.NotContains(t, mergeRefusal("734253d9a", "main", jobtypes.Job{ID: "worker"}), "integrates")
}

func TestIntegrationHolderMergesItsBranchesOnEveryPath(t *testing.T) {
	row := jobtypes.Job{ID: "integrate", Integrates: []string{"done-a"}}
	got := map[string]bool{}
	for _, operand := range []string{"done-a", "other"} {
		got[operand] = mergesBase(operand, "", row)
	}
	assert.Equal(t, map[string]bool{"done-a": true, "other": false}, got)
	assert.False(t, mergesBase("done-a", "", jobtypes.Job{ID: "worker"}), "a row integrating nothing merges nothing")
}

// standingWorker is a worker row on a checkout its fork made. Each test gets a
// fresh ledger.
type standingWorker struct {
	suite.Suite
	layers ctxLayers
	repo   worktreeRepo
	own    string
	worker jobtypes.Job
}

func (s *standingWorker) build() {
	s.repo = newWorktreeRepository(s.T())
	s.own = s.repo.add("lease-job")
	s.worker = narrowLease()
	s.worker.Parent = "orchestrator"
	s.worker.CheckoutRoot = s.own
	s.worker.Worktree = &jobtypes.Worktree{Root: s.own}
}

func (s *standingWorker) ledger() {
	s.layers, _ = fleetLayers(s.T(), s.worker)
	s.layers = append(s.layers, snapshotLayer(job.Snapshot{Rows: []jobtypes.Job{s.worker}}))
}

// judgedWorker is a standingWorker whose suite's tests only judge, so they
// share one repository and fail if one of them changed it.
type judgedWorker struct {
	standingWorker
	shared *testkit.SharedRepository
}

func (s *judgedWorker) SetupSuite() {
	s.build()
	s.shared = s.repo.share()
}

func (s *judgedWorker) SetupTest() {
	s.repo = s.repo.of(s.T())
	s.ledger()
}

func (s *judgedWorker) TearDownTest() { s.shared.AssertUnchanged(s.T()) }

func (s *standingWorker) from(dir, line string) string {
	return denyLeaseScopedVCS(s.testContext(), Dependencies{callDir: dir}, s.worker.ID, line)
}

type WorkerMoveSuite struct{ judgedWorker }

func TestWorkerMoveSuite(t *testing.T) { suite.Run(t, new(WorkerMoveSuite)) }

func (s *WorkerMoveSuite) spelledOut() string {
	return fmt.Sprintf("`git -C %s mv <old> <new>`", s.own)
}

func (s *WorkerMoveSuite) TestALoneMovePasses() {
	s.Empty(s.from(s.own, "git mv a.txt b.txt"))
}

func (s *WorkerMoveSuite) TestAChainedMovePasses() {
	s.Empty(s.from(s.own, "git mv a.txt b.txt; git mv c.txt d.txt"))
}

func (s *WorkerMoveSuite) TestALoopOverLiteralWordsPasses() {
	s.Empty(s.from(s.own, "for f in a.txt b.txt; do git mv $f new/$f; done"))
}

func (s *WorkerMoveSuite) TestAQuotedLoopVariablePasses() {
	s.Empty(s.from(s.own, `for f in a.txt b.txt; do git mv "$f" "new/$f"; done`))
}

func (s *WorkerMoveSuite) TestALoopClimbingOutIsRefused() {
	reason := s.from(s.own, "for f in a.txt; do git mv $f ../$f; done")
	s.Contains(reason, "a worker moves only paths its line spells out")
	s.Contains(reason, s.spelledOut())
}

func (s *WorkerMoveSuite) TestAnUnboundVariableIsRefusedWithTheSpelledOutLine() {
	reason := s.from(s.own, `git mv "$src" "$dst"`)
	s.Contains(reason, "a worker moves only paths its line spells out")
	s.Contains(reason, s.spelledOut())
}

// The word renders as `.txt`, a path the shell would not pass.
func (s *WorkerMoveSuite) TestAVariableBesideTextIsRefused() {
	s.Contains(s.from(s.own, "git mv $src.txt new.txt"), "not one literal string")
}

func (s *WorkerMoveSuite) TestALoopOverACommandSubstitutionIsRefused() {
	s.Contains(s.from(s.own, "for f in $(ls); do git mv $f new/$f; done"), "where it moves cannot be read")
}

func (s *WorkerMoveSuite) TestAShellPayloadIsRefused() {
	s.Contains(s.from(s.own, `sh -c 'for f in a; do git mv $f.txt outside/$f.txt; done'`), "shell payload")
}

func (s *WorkerMoveSuite) TestAMoveInAConditionalIsRefusedAsAMove() {
	reason := s.from(s.own, "if true; then git mv a.txt b.txt; fi")
	s.Contains(reason, "a worker moves only paths its line spells out")
	s.NotContains(reason, "a worker commits only")
}

func (s *WorkerMoveSuite) TestAMoveFromAnotherCheckoutNamesItsOwn() {
	s.Contains(s.from(s.repo.main, "git mv a.txt b.txt"), fmt.Sprintf("`git -C %s mv a.txt b.txt`", s.own))
}

func (s *WorkerMoveSuite) TestALoopFromAnotherCheckoutIsRefused() {
	s.Contains(s.from(s.repo.main, "for f in a.txt; do git mv $f new/$f; done"), "outside "+s.own)
}

func (s *WorkerMoveSuite) TestAnAliasNamedMvStaysAnAlias() {
	s.Contains(s.from(s.own, "git -c alias.mv=push mv origin"), "`git -c alias.mv`")
}

type WorkerReattachSuite struct{ judgedWorker }

func TestWorkerReattachSuite(t *testing.T) { suite.Run(t, new(WorkerReattachSuite)) }

func (s *WorkerReattachSuite) TestSwitchForceCreateOnItsOwnBranchIsRefused() {
	s.Contains(s.from(s.own, "git switch -C "+s.worker.ID), "`git switch`")
}

func (s *WorkerReattachSuite) TestCheckoutBOnItsOwnBranchIsRefused() {
	s.Contains(s.from(s.own, "git checkout -B "+s.worker.ID), "`git checkout`")
}

func (s *WorkerReattachSuite) TestAnExplicitHEADStillRefusesForceCreate() {
	s.Contains(s.from(s.own, fmt.Sprintf("git switch -C %s HEAD", s.worker.ID)), "`git switch`")
}

func (s *WorkerReattachSuite) TestAnotherBranchNameIsRefused() {
	s.Contains(s.from(s.own, "git switch -C other"), "`git switch`")
}

func (s *WorkerReattachSuite) TestTheBaseBranchIsRefused() {
	s.Contains(s.from(s.own, "git checkout -B main"), "`git checkout`")
}

func (s *WorkerReattachSuite) TestAnotherStartPointIsRefused() {
	s.Contains(s.from(s.own, fmt.Sprintf("git switch -C %s origin/main", s.worker.ID)), "`git switch`")
}

func (s *WorkerReattachSuite) TestAForceIsRefused() {
	s.Contains(s.from(s.own, fmt.Sprintf("git checkout -f -B %s", s.worker.ID)), "`git checkout`")
}

func (s *WorkerReattachSuite) TestAMergeIsRefused() {
	s.Contains(s.from(s.own, fmt.Sprintf("git switch -C %s --merge", s.worker.ID)), "`git switch`")
}

func (s *WorkerReattachSuite) TestAVariableBesideTheNameIsRefused() {
	s.Contains(s.from(s.own, fmt.Sprintf(`git switch -C "$prefix"%s`, s.worker.ID)), "`git switch`")
}

func (s *WorkerReattachSuite) TestForceCreateInAnotherCheckoutIsRefused() {
	s.Contains(s.from(s.repo.main, "git switch -C "+s.worker.ID), "`git switch`")
}

func (s *WorkerReattachSuite) TestARowWithNoCheckoutOfItsOwnIsRefused() {
	row := s.worker
	row.Worktree = nil
	ctx := job.WithSnapshot(s.testContext(), job.Snapshot{Rows: []jobtypes.Job{row}})
	s.Contains(denyLeaseScopedVCS(ctx, Dependencies{callDir: s.own}, row.ID, "git switch -C "+row.ID), "`git switch`")
}

// DetachedWorkerSuite detaches the worker's checkout, so each test builds a
// repository of its own.
type DetachedWorkerSuite struct{ standingWorker }

func TestDetachedWorkerSuite(t *testing.T) { suite.Run(t, new(DetachedWorkerSuite)) }

func (s *DetachedWorkerSuite) SetupTest() {
	s.build()
	s.worker.Authority = "rev-a"
	s.ledger()
}

func (c *remedyContractSuite) TestLeaseVcs() {
	s := new(DetachedWorkerSuite)
	s.SetT(c.T())
	s.SetupTest()
	s.repo.git(s.own, "switch", "--detach")
	const rel = "cmd/magus/change.txt"
	target := filepath.Join(s.own, rel)
	s.Require().NoError(os.MkdirAll(filepath.Dir(target), 0o755))
	s.Require().NoError(os.WriteFile(target, []byte("old\n"), 0o644))
	patch := filepath.Join(s.T().TempDir(), "change.patch")
	s.Require().NoError(os.WriteFile(patch, []byte(gitDiffTouching(rel)), 0o644))
	line := "git apply " + patch
	deps := withSetting(string(denyRuleStall), builtin.Advise)
	req := Request{Input: line, Dir: s.own, Session: "s", Host: "test-host", Lease: s.worker.ID}
	block := Judge(s.testContext(), deps, req)
	s.Require().Equal(string(denyRuleLeaseVCS), block.Rule, block.Reason)
	var stderr strings.Builder
	s.Require().Error(verdictExit(&stderr, output.Options{Format: output.JSON}, block))
	s.T().Logf("worker first line: %s", stderr.String())
	s.Contains(stderr.String(), "create the job branch at this HEAD")
	s.NotContains(block.Reason, "Only the root")
	gate := stallGate(hookLocation(s.testContext(), deps).cacheDir, s.worker.ID)
	s.Equal(1, readStall(gate).Level)
	s.Require().NotEmpty(block.NextSteps)
	want := []string{"git", "-C", s.own, "switch", "-c", s.worker.ID}
	s.Equal(want, block.NextSteps[0].Argv)
	gradeServed(s.T(), s.testContext(), deps, req, "lease holder", block.NextSteps[0].ID, block.NextSteps[0].Run)
	s.repo.git(s.own, want[1:]...)
	retry := Judge(s.testContext(), deps, req)
	s.Require().NotEqual("deny", string(retry.Decision), retry.Reason)
	s.repo.git(s.own, "apply", patch)
	contents, err := os.ReadFile(target)
	s.Require().NoError(err)
	s.Equal("new\n", string(contents))
}

func (s *DetachedWorkerSuite) TestADetachedCheckoutWithoutADestinationNamesTheOrchestrator() {
	s.repo.git(s.own, "switch", "--detach")
	row := s.worker
	row.Worktree = nil
	ctx := job.WithSnapshot(s.testContext(), job.Snapshot{Rows: []jobtypes.Job{row}})
	deps := withSetting(string(denyRuleStall), builtin.Advise)
	block := Judge(ctx, deps, Request{Input: "git commit -m done", Dir: s.own, Session: "s", Host: "test-host", Lease: row.ID})
	s.Require().Equal(string(denyRuleLeaseVCS), block.Rule, block.Reason)
	var stderr strings.Builder
	s.Require().Error(verdictExit(&stderr, output.Options{Format: output.JSON}, block))
	line := stderr.String()
	s.T().Logf("orchestrator first line: %s", line)
	s.Contains(line, "ask the orchestrator to record a writable linked checkout")
	s.Contains(line, "Only the root can clear this")
	s.NotContains(line, "git switch")
	gate := stallGate(hookLocation(ctx, deps).cacheDir, row.ID)
	s.Equal(3, readStall(gate).Level)
}

// UnboundCommitSuite is a worker whose row has a checkout from its fork, before
// job exec stamped checkout_root there.
type UnboundCommitSuite struct{ judgedWorker }

func TestWorkerVCSUnboundCommitSuite(t *testing.T) { suite.Run(t, new(UnboundCommitSuite)) }

func (s *UnboundCommitSuite) commit() string {
	row := s.worker
	row.CheckoutRoot = ""
	ctx := job.WithSnapshot(s.testContext(), job.Snapshot{Rows: []jobtypes.Job{row}})
	return denyLeaseScopedVCS(ctx, Dependencies{callDir: s.own}, row.ID, "git commit -m done")
}

func (s *UnboundCommitSuite) TestTheRefusalNamesJobExecInTheForkedCheckout() {
	s.Contains(s.commit(), fmt.Sprintf("run `cd %s && %s`, then commit there.", s.own, hint.JobExec.With(s.worker.ID)))
}

func (s *UnboundCommitSuite) TestAnUnboundCommitIsStillRefused() {
	s.Contains(s.commit(), "a worker commits only on its own branch")
}

// LeaseWriteMoveSuite grades what a move writes against a row's write paths.
type LeaseWriteMoveSuite struct {
	suite.Suite
	layers ctxLayers
}

func TestLeaseWriteMoveSuite(t *testing.T) { suite.Run(t, new(LeaseWriteMoveSuite)) }

func (s *LeaseWriteMoveSuite) hold(writes []string, denies ...string) {
	layers, root := fleetLayers(s.T(), jobtypes.Job{ID: "mover", Paths: jobtypes.Paths{Write: writes, Deny: denies}, State: jobtypes.StateRunning, Registered: 1})
	s.layers = layers
	for _, file := range []string{"libs/old/a.go", "libs/old/sub/b.go"} {
		s.Require().NoError(os.MkdirAll(filepath.Join(root, filepath.Dir(file)), 0o755))
		s.Require().NoError(os.WriteFile(filepath.Join(root, file), nil, 0o644))
	}
}

func (s *LeaseWriteMoveSuite) deny(line string) string {
	return denyWriteOutsideLease(s.testContext(), Dependencies{}, "mover", line)
}

func (s *LeaseWriteMoveSuite) TestEveryFileOnTheRowUnderBothNamesPasses() {
	s.hold([]string{"libs/old/a.go", "libs/old/sub/b.go", "libs/new/a.go", "libs/new/sub/b.go"})
	s.Empty(s.deny("git mv libs/old libs/new"))
}

func (s *LeaseWriteMoveSuite) TestALandingOffTheRowIsNamed() {
	s.hold([]string{"libs/old/a.go", "libs/old/sub/b.go", "libs/new/a.go"})
	s.Contains(s.deny("git mv libs/old libs/new"), "libs/new holds libs/new/sub/b.go, which is outside")
}

func (s *LeaseWriteMoveSuite) TestALandingOnADenyPathIsNamed() {
	s.hold([]string{"libs/old/**", "libs/new/**"}, "libs/new/sub/**")
	s.Contains(s.deny("git mv libs/old libs/new"), "libs/new holds libs/new/sub/b.go, which is outside")
}

func (s *LeaseWriteMoveSuite) TestASourceFileOffTheRowIsNamed() {
	s.hold([]string{"libs/old/a.go", "libs/new/a.go", "libs/new/sub/b.go"})
	s.Contains(s.deny("git mv libs/old libs/new"), "libs/old holds libs/old/sub, which is outside")
}

func (s *LeaseWriteMoveSuite) TestAFileMovedToAnUndeclaredNameIsRefused() {
	s.hold([]string{"libs/old/a.go", "libs/new/a.go"})
	s.Contains(s.deny("git mv libs/old/a.go libs/new"), "libs/new is outside")
}

func (s *LeaseWriteMoveSuite) TestALoopToAnOutsidePathIsRefused() {
	s.hold([]string{"libs/old/a.go"})
	s.Contains(s.deny("for f in a.go; do git mv libs/old/$f libs/elsewhere/$f; done"), "libs/elsewhere/a.go is outside")
}

func (s *LeaseWriteMoveSuite) TestALoopWithTextBesideItsVariableIsGraded() {
	s.hold([]string{"libs/old/a.go"})
	s.Contains(s.deny("for f in a; do git mv libs/old/$f.go libs/elsewhere/$f.go; done"), "libs/elsewhere/a.go is outside")
}

func (s *LeaseWriteMoveSuite) TestAWrappedLoopToAnOutsidePathIsRefused() {
	s.hold([]string{"libs/old/a.go"})
	s.Contains(s.deny("for f in a.go; do env X=1 git mv libs/old/$f libs/elsewhere/$f; done"), "libs/elsewhere/a.go is outside")
}

func (s *LeaseWriteMoveSuite) TestALoopFromOutsideTheRowIsRefused() {
	s.hold([]string{"libs/new/a.go"})
	s.Contains(s.deny("for f in a.go; do git mv libs/old/$f libs/new/$f; done"), "libs/old/a.go is outside")
}

func (s *LeaseWriteMoveSuite) TestALoopedRemoveIsGraded() {
	s.hold([]string{"libs/new/a.go"})
	s.Contains(s.deny("for f in a; do git rm libs/old/$f.go; done"), "libs/old/a.go is outside")
}

func (s *LeaseWriteMoveSuite) TestALoopInsideTheRowPasses() {
	s.hold([]string{"libs/old/a.go", "libs/new/a.go"})
	s.Empty(s.deny("for f in a.go; do git mv libs/old/$f libs/new/$f; done"))
}

// A claim covers its file at the path level, so moving the file passes.
func (s *LeaseWriteMoveSuite) TestAClaimOnOneDeclarationCoversAMoveOfItsFile() {
	s.hold([]string{"libs/old/a.go#project.name", "libs/new/a.go"})
	s.Empty(s.deny("git mv libs/old/a.go libs/new/a.go"))
}

func (s *LeaseWriteMoveSuite) testContext() context.Context { return s.layers.of(s.T()) }

func (s *standingWorker) testContext() context.Context { return s.layers.of(s.T()) }

func ledgerAt(t *testing.T, ctx context.Context) location {
	t.Helper()
	at, ok := ctx.Value(locationKey{}).(location)
	require.True(t, ok)
	return at
}

// contextWithLedger lets a suite keep the ledger location instead of a context field.
func contextWithLedger(t *testing.T, at location) context.Context {
	return withSecrets(context.WithValue(t.Context(), locationKey{}, at))
}

// workerRevert pins row 602: a worker reverts its own commit on its own
// branch, graded like a cherry-pick by the paths it writes. Each test gets a
// fresh ledger.
type workerRevert struct {
	suite.Suite
	at     location
	repo   worktreeRepo
	worker jobtypes.Job
	wt     string
}

func (s *workerRevert) build() {
	_, s.repo, s.worker, s.wt = leaseVCSRepository(s.T())
}

func (s *workerRevert) ledger() {
	ctx, _ := fleetFixture(s.T(), s.worker)
	s.at = ledgerAt(s.T(), ctx)
}

func (s *workerRevert) judge(command string) string {
	return denyLeaseScopedVCS(contextWithLedger(s.T(), s.at), Dependencies{callDir: s.wt}, s.worker.ID, command)
}

// WorkerRevertSuite judges reverts of the base commit. Its tests only judge,
// so they share one repository and fail if one of them changed it.
type WorkerRevertSuite struct {
	workerRevert
	shared *testkit.SharedRepository
}

func TestWorkerRevertSuite(t *testing.T) { suite.Run(t, new(WorkerRevertSuite)) }

func (s *WorkerRevertSuite) SetupSuite() {
	s.build()
	s.shared = s.repo.share()
}

func (s *WorkerRevertSuite) SetupTest() {
	s.repo = s.repo.of(s.T())
	s.ledger()
}

func (s *WorkerRevertSuite) TearDownTest() { s.shared.AssertUnchanged(s.T()) }

// WorkerRevertOwnCommitSuite commits on the worker's branch, so each test
// builds a repository of its own.
type WorkerRevertOwnCommitSuite struct{ workerRevert }

func TestWorkerRevertOwnCommitSuite(t *testing.T) { suite.Run(t, new(WorkerRevertOwnCommitSuite)) }

func (s *WorkerRevertOwnCommitSuite) SetupTest() {
	s.build()
	s.ledger()
}

func (s *WorkerRevertOwnCommitSuite) TestRevertsItsOwnCommit() {
	s.Require().NoError(os.MkdirAll(filepath.Join(s.wt, "cmd", "magus"), 0o755))
	s.repo.commitFiles(s.wt, map[string]string{"cmd/magus/x.go": "package main\n"})
	s.Empty(s.judge("git revert --no-edit HEAD"))
}

func (s *WorkerRevertSuite) TestIsRefusedARevertOutsideItsWritePaths() {
	s.Contains(s.judge("git revert --no-edit HEAD"), "a.txt", "the base commit wrote a.txt")
}

func (s *WorkerRevertSuite) TestIsRefusedARebase() {
	s.NotEmpty(s.judge("git rebase origin/main"), "only an integrator rebases")
}

// WorkerRebaseSuite pins that a worker rebases its own job branch onto a ref its row names.
// It never rebases another branch, or onto a ref the row does not name.
type WorkerRebaseSuite struct{ workerRevert }

func TestWorkerRebaseSuite(t *testing.T) { suite.Run(t, new(WorkerRebaseSuite)) }

func (s *WorkerRebaseSuite) SetupTest() {
	s.build()
	s.worker.ID = "lease-job"
	s.worker.Checkpoint = s.repo.origin.Git("-C", s.wt, "rev-parse", "HEAD")
	s.worker.DependsOn = []string{"done-a"}
	s.ledger()
}

func (s *WorkerRebaseSuite) TestRebasesItsBranchOntoARefItsRowNames() {
	for _, command := range []string{
		"git rebase " + s.worker.Checkpoint[:12],
		"git rebase done-a",
		"git rebase --onto origin/done-a HEAD~1",
		"git rebase --continue",
	} {
		s.Empty(s.judge(command), "%q", command)
	}
}

func (s *WorkerRebaseSuite) TestIsRefusedARebaseOntoARefItsRowDoesNotName() {
	reason := s.judge("git rebase origin/main")
	s.Contains(reason, "It rebases onto origin/main")
	s.Contains(reason, "done-a")
}

func (s *WorkerRebaseSuite) TestIsRefusedARebaseOfAnotherBranch() {
	s.repo.git(s.wt, "checkout", "-q", "-b", "other-job")
	s.Contains(s.judge("git rebase done-a"), "only its own job's branch, lease-job")
	s.NotEmpty(s.judge("git rebase done-a lease-job"), "a second operand switches branches first")
}
