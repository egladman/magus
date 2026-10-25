package guard

import (
	"testing"

	"github.com/egladman/magus/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			// The fast-forward to a placed binary's commit, named by its id.
			"git merge --ff-only f1980cc5c", "git merge f1980cc5c3224cc9fca99c41a9f03415fd77db4b --ff-only",
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

func TestIntegrationHolderMergesItsBranchesOnEveryPath(t *testing.T) {
	row := types.Job{ID: "integrate", Integrates: []string{"done-a"}}
	got := map[string]bool{}
	for _, operand := range []string{"done-a", "other"} {
		got[operand] = mergesBase(operand, "", row)
	}
	assert.Equal(t, map[string]bool{"done-a": true, "other": false}, got)
	assert.False(t, mergesBase("done-a", "", types.Job{ID: "worker"}), "a row integrating nothing merges nothing")
}
