package guard

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
			"git rm a.go":                         "git rm",
			"git mv a.go b.go":                    "git mv",
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

	t.Run("anything else is not version control", func(t *testing.T) {
		assert.Empty(t, op(t, "rm -rf build"))
	})
}
