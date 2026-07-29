package doctor

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// worktreesDirRel is where the agent tooling parks per-branch checkouts. It is
// inside the workspace, so anything left there is walked as if it were source.
const worktreesDirRel = ".claude/worktrees"

// checkStaleWorktrees reports checkout directories under .claude/worktrees that git
// no longer tracks.
//
// A leftover directory is a second copy of the workspace's spell sources, so magus
// discovers them twice at the repo root: it trips MGS1002 (spell shadowed) and fails
// tests that walk the tree, with a failure that points at the duplicate rather than
// at the cause.
//
// Workspaces that hit this have tended to write "remove dead worktrees first" into
// their contributor notes, which makes it a rule a human has to remember. A check
// costs nothing to run and reports the cause directly, which is the whole argument
// for it being here rather than in prose.
//
// Detection is by absence of the .git link file that git writes into a live
// worktree and removes on `git worktree remove`/`prune`. A pruned-but-not-deleted
// directory therefore reads as stale without shelling out to git, which keeps the
// check cheap enough to run every time and correct when git is unavailable.
func checkStaleWorktrees(root string) Check {
	const name = "stale worktrees"
	dir := filepath.Join(root, filepath.FromSlash(worktreesDirRel))

	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return Check{Name: name, Status: StatusOK, Message: "no " + worktreesDirRel + " directory"}
		}
		return Check{Name: name, Status: StatusFail, Message: fmt.Sprintf("scan %s: %v", worktreesDirRel, err)}
	}

	var stale []string
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// A live worktree carries a .git FILE pointing at the real gitdir; a directory
		// git has forgotten has neither.
		if _, statErr := os.Stat(filepath.Join(dir, e.Name(), ".git")); statErr == nil {
			continue
		}
		stale = append(stale, e.Name())
	}
	if len(stale) == 0 {
		return Check{Name: name, Status: StatusOK, Message: fmt.Sprintf("%d live worktree(s); none orphaned", len(entries))}
	}
	sort.Strings(stale)

	details := make([]string, 0, len(stale)+1)
	for _, s := range stale {
		details = append(details, filepath.Join(worktreesDirRel, s))
	}
	details = append(details, "remove the directory, or restore it with `git worktree add` if the work is still wanted")
	return Check{
		Name:    name,
		Status:  StatusFail,
		Message: fmt.Sprintf("%d directory(ies) under %s are not live worktrees; they duplicate spell sources and can trip MGS1002 and fail tests", len(stale), worktreesDirRel),
		Details: details,
	}
}
