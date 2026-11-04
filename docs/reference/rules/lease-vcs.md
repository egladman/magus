---
title: "lease-vcs: a worker lease mutating version control, or committing outside its own branch and checkout"
description: "A deny rule by default: it refuses a worker lease mutating version control, or committing outside its own branch and checkout, and names what to run instead."
tags: [guard, rules, lease-vcs, deny]
---

# lease-vcs

A deny rule by default: it refuses a worker lease mutating version control, or committing outside its own branch and checkout, and names what to run instead.

## What it catches

A worker lease mutating version control, or committing outside its own branch and checkout.

## Why

The orchestrator lands every unit from the worker's tree, so a worker that pushes, stashes, reverts, resets, cleans, rebases, merges or removes a worktree changes the state it is integrated from, and a whole-tree revert destroys a sibling's uncommitted work. A worker may run only what reads the repository or touches its own index and branch: status, diff, log and the other reads, add, restore of named paths, switch or checkout without a force, branch creation, a fetch into remote-tracking refs; any other verb, an alias from config included, is refused. A remove or move of named paths (git rm and mv, hg and sl remove and rename) is allowed too, and lease-write grades each path as it grades rm and mv; one whose paths the line does not spell out, through `.`, a glob, pathspec magic, an hg pattern or a list read from a file, is refused. A move gets one verdict however the line repeats it: alone, chained, or in a `for` loop over literal words, whose variable is read as each word and whose paths lease-write grades one by one. A word left unread, a shell payload or a call in a conditional is refused and names `git -C <checkout> mv <old> <new>`. A directory move to a new name is graded by the files landing under that name. A commit is the one exception beyond those: allowed only in the lease's checkout_root, when that is a secondary checkout on a named branch other than the base, with any backend. A git cherry-pick and a git apply are graded as a commit is, since `git diff | git apply -3 && git commit -C` lands the same change as the cherry-pick. A shell script the line runs is judged line by line, so a commit or a reset inside it gets the same verdict as one typed on the line; a script not on disk yet cannot be read, so it is refused: write it, then run it in a call of its own. On a detached HEAD the refusal names `git switch <job>`, or `git switch -c <job>` when the orchestrator made no branch for the job; a row with its own standing checkout gets `git switch -C <job>`, which keeps the detached commits. That line and `git checkout -B <job>` are allowed only for the row's own id, in its own checkout, with no start point but HEAD and no force or merge. Every spelling is placed alike: `git -C <dir>`, `--git-dir`, `GIT_DIR=`, a `cd` before it, and vcs\cmd from a Buzz script under the lease. Paths compare by device and inode, so a case variant or a firmlinked spelling of a checkout is that checkout. A commit whose checkout cannot be read is refused, since it cannot be shown to be the worker's own. vcs\cmd may also run any git operation but a push in a repository standing wholly outside the job's checkout, such as a script's scratch repository: its directory, -C and the work tree a --git-dir implies are outside the checkout, its common git directory is not the job's, git reports that directory as its work tree, and neither -c nor --config-env nor GIT_DIR, GIT_WORK_TREE, GIT_COMMON_DIR or GIT_INDEX_FILE is set. A worker may create a repository in a directory that neither lies in the lease's checkout nor belongs to a checkout of its repository, with no GIT_DIR, --git-dir or -C pointing back into it, and may run version control inside what it creates on the same line. A scratch repository inside the checkout, under .magus/ say, is refused like a submodule there: it holds the job's files under another git directory. proc\exec and proc\shell refuse git, hg, sl and jj outright and name the vcs\cmd call to make instead, so a Buzz script's version control is judged here. A worker is a row with a parent, a lease a subagent holds, or a caller that names no session; only an identified root session holding a parentless row is the root.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"lease-vcs": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [lease-vcs]: ...
```

A workspace that sets this rule to `advise` sees `advise [lease-vcs]: ...` instead.

`magus describe rule lease-vcs` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
