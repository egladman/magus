package guard

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/egladman/magus/internal/gen/typestest"
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
	err := vcsCommandRefusal(s.ctx, s.worker, "git", []string{"cherry-pick", picked}, s.dir, nil)
	s.Require().Error(err)
	s.Contains(err.Error(), "It picks commits writing gen.json")
}

func (s *commitGradeRoutesSuite) TestAPickInsideTheWritePathsPasses() {
	s.Require().NoError(os.MkdirAll(filepath.Join(s.repo.main, "cmd/magus"), 0o755))
	picked := s.repo.commitFiles(s.repo.main, map[string]string{"cmd/magus/x.go": "x\n"})
	s.Empty(s.deny("git cherry-pick " + picked))
}

func (s *commitGradeRoutesSuite) TestAPickFromStandardInputIsRefusedThroughTheVCSCommand() {
	err := vcsCommandRefusal(s.ctx, s.worker, "git", []string{"cherry-pick", "--stdin"}, s.dir, nil)
	s.Require().Error(err)
	s.Contains(err.Error(), "Name each commit in the call")
}

func (s *commitGradeRoutesSuite) TestContinuingAPickPasses() {
	s.Empty(s.deny("git cherry-pick --continue"))
}

func (s *commitGradeRoutesSuite) TestAnIntegrationRowPicksWhatItsWritePathsCover() {
	picked := s.repo.commitFiles(s.repo.main, map[string]string{"gen.json": "x\n"})
	row := s.worker
	row.WritePaths = append(row.WritePaths, "gen.json")
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
	s.Contains(s.deny("bash "+script), "`git rebase` stays with the orchestrator")
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
	s.Empty(denyLeaseScopedVCS(s.ctx, Dependencies{callDir: s.dir}, "no-such-lease", "sh "+script))
}

func (s *commitGradeRoutesSuite) TestAVerbInANestedScriptIsRefused() {
	inner := s.script("inner.sh", "git rebase main\n")
	outer := s.script("outer.sh", fmt.Sprintf("sh %s\n", inner))
	s.Contains(s.deny("sh "+outer), "`git rebase` stays with the orchestrator")
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
	s.Contains(s.deny("cd tools && ./run.sh"), "`git rebase` stays with the orchestrator")
	s.Empty(s.deny("./run.sh"))
}

func (s *commitGradeRoutesSuite) TestAScriptRunsItsOwnRelativeCallsFromTheCd() {
	s.scriptIn("tools", "outer.sh", "./inner.sh\n")
	s.scriptIn("tools", "inner.sh", "git rebase main\n")
	s.Contains(s.deny("cd tools && ./outer.sh"), "`git rebase` stays with the orchestrator")
}

func (s *commitGradeRoutesSuite) TestAScriptNameIsJudgedInEachDirectoryItRuns() {
	s.script("run.sh", "git status --short\n")
	s.scriptIn("tools", "run.sh", "git rebase main\n")
	s.Contains(s.deny("./run.sh && cd tools && ./run.sh"), "`git rebase` stays with the orchestrator")
}

func (s *commitGradeRoutesSuite) TestARelativeScriptAfterAnUnknownCdIsRefused() {
	s.script("ok.sh", "git status --short\n")
	s.Contains(s.deny(`cd "$WHERE" && ./ok.sh`), "from a directory only running the line can reveal")
}

func (s *commitGradeRoutesSuite) TestAnAbsoluteScriptAfterAnUnknownCdIsRead() {
	script := s.script("ok.sh", "git status --short\n")
	s.Empty(s.deny(fmt.Sprintf(`cd "$WHERE" && %s`, script)))
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
	row := types.Job{DependsOn: []string{"worker-a", "worker-b"}}
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
		assert.False(t, mergesBase("worker-a", "main", types.Job{}))
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
	row := types.Job{ID: "integrate", Integrates: []string{"done-a", "done-b"}}
	merge := func(line string, row types.Job) bool {
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
		"git merge done-a", "git merge --no-ff other", "git merge --no-ff done-a done-b",
		"git merge --no-ff -X theirs done-a", "git merge --no-ff --squash done-a",
		"git -C ../x merge --no-ff done-a", "git rebase --no-ff done-a", "hg merge --no-ff done-a",
	} {
		assert.False(t, merge(line, row), line)
	}
	assert.False(t, merge("git merge --no-ff done-a", types.Job{ID: "worker"}), "a row integrating nothing merges nothing")
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
	row := types.Job{ID: "integrate", Integrates: []string{"done-a", "done-b"}}
	assert.Contains(t, mergeRefusal("734253d9a", "main", row), "or a branch it integrates, merged by name: done-a, done-b.")
}

func TestMergeRefusalOfARowIntegratingNothingNamesNoBranch(t *testing.T) {
	assert.NotContains(t, mergeRefusal("734253d9a", "main", types.Job{ID: "worker"}), "integrates")
}

func TestIntegrationHolderMergesItsBranchesOnEveryPath(t *testing.T) {
	row := types.Job{ID: "integrate", Integrates: []string{"done-a"}}
	got := map[string]bool{}
	for _, operand := range []string{"done-a", "other"} {
		got[operand] = mergesBase(operand, "", row)
	}
	assert.Equal(t, map[string]bool{"done-a": true, "other": false}, got)
	assert.False(t, mergesBase("done-a", "", types.Job{ID: "worker"}), "a row integrating nothing merges nothing")
}
