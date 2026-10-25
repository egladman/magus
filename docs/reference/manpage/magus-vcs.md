---
title: magus vcs
generated_from: internal/commands/registry.go
description: "List the dirty paths a commit of this change wants, read against the workspace's declarations, and settle an in-progress merge's conflicted generated files by regenerating them once."
tags: [cli, magus vcs, git, merge, conflicts, generated files, staging, ls-files]
---

# magus-vcs

Listing and conflict resolution that knows what is generated

## Synopsis

**magus** vcs \<ls-files|resolve|checkpoint|merge-driver\> [flags]

## Description

Version-control operations that read the workspace's output declarations,
so generated files and hand-written sources are treated differently.

ls-files lists dirty paths and stages nothing; you stage what it lists. By
default it lists tracked, modified or deleted declared sources that no other
live job holds and that are not already staged. Every other dirty path is left
out by a group, and stderr names each group with the flag that lists it:
--include-untracked, --include-generated (outputs a dirty source explains),
--include-unexplained (outputs nothing here explains, with the drift code),
--include-unclaimed (paths no target declares, and the ones magus maintains),
--include-held (paths another live job writes) and --include-staged. A backend
that keeps no such state refuses the flag: only git stages, and jj has no
untracked files.

Positional patterns and --exclude take write-path patterns (a literal path,
a directory, or a glob with \* and \*\*), read from the current directory and
matched the same way on every backend. --project scopes to the files a project
owns. Patterns only narrow; a pattern that matches no dirty path is said on
stderr. Stage the listing with:

magus vcs ls-files -o name | git add --pathspec-from-file=-

resolve settles an in-progress merge, rebase, or cherry-pick. It classifies
every conflicted path at once, regenerates once instead of once per file,
settles the files one side deleted (which no VCS invokes a merge driver for),
and stages everything the regeneration touched so a following commit or
rebase --continue does not refuse on a dirty tree. Conflicts in files magus
does not generate are reported and left alone. Pass --against \<ref\> to merge
that ref first and settle what it conflicts with; add --dry-run to see the
classification and have the merge backed out again.

--hook \<name\> is how git runs resolve itself. magus init --vcs git writes
the pre-merge-commit, pre-commit, post-commit, post-rewrite and
post-applypatch hooks beside the merge driver, each calling resolve once the
merge, cherry-pick, revert, rebase or am has the whole tree; magus doctor
reports a driver whose hooks are missing. It regenerates only the projects
whose declared sources or outputs the operation changed, plus what the merge
driver recorded, and stages the result: into the commit git is about to make,
or, for a clean merge, in place of git's own commit, which git then leaves to
you as "git commit". After a rebase or a clean cherry-pick the commits already
exist, so it stages and prints the amend; it never amends. A failed
regeneration stops a pending commit and prints the command that does it.

merge-driver is the per-file driver git and hg invoke during a merge. You do
not run it by hand; it is wired per clone, because a driver registration
cannot be committed. That is also why a forge reports conflicts your own
clone would settle silently, and why resolve exists as the bulk counterpart.

checkpoint prints the identity of the working state right now - head revision,
branch, whether the tree is dirty, and a digest of the uncommitted patch. Record
one when you hand a piece of work out, so a later reader knows what that work was
looking at. By default it RESOLVES AND RECORDS and never MINTS: no tag, no stash,
no ref, no file, nothing changed anywhere, so a checkpoint nobody keeps has cost
nothing. Feed the revision to anything that takes one; compare two digests to learn
whether two workers saw the same uncommitted tree, which the revision alone cannot
say.

--preserve is the one thing that mints. An identity says whether two trees match;
it cannot rebuild either one. --preserve additionally captures the uncommitted
work - tracked edits and untracked files alike - and prints a handle that gets it
back, using each backend's own mechanism: a commit under refs/magus/preserved for
git, a kept shelf for Mercurial, a commit Sapling keeps hidden, and for Jujutsu the
working-copy commit it already holds. The working copy is untouched either way.

Retention differs per backend. On git and Mercurial a capture is dropped at 30
days, by two passes that cover each other's gap: every preserve prunes, which
bounds a repository nothing schedules against, and the server's prune-preserved
job prunes on its own, which reaches a repository preserved once and never
again. That job is a no-op with no server running, so on a machine that runs
none the standalone spelling is magus server prune-preserved.

Sapling drops nothing either way: magus mints a hidden commit there, and no
Sapling command removes one without discarding the working copy, so those stay
until you remove them. List them with sl log --hidden -r "desc('magus preserved
working copy')". Jujutsu mints nothing, so nothing accumulates.

resolve works on git, Mercurial and Jujutsu. Only --against is git-only: merge the
base in yourself on the others, then run resolve.

### vcs ls-files options

**--exclude** *pattern*
: Leave out paths this write-path pattern covers; repeatable or comma-separated

**--include-generated**
: Also list declared outputs a dirty source in this change explains

**--include-held**
: Also list paths another live job in this checkout writes

**--include-staged**
: Also list paths whose whole change is already staged; git only

**--include-unclaimed**
: Also list paths no target declares, and the ones magus maintains itself

**--include-unexplained**
: Also list declared outputs no source change here explains

**--include-untracked**
: Also list untracked files; refused on jj, which has none

**--project** *project*
: List only the files this project owns; repeatable or comma-separated

### vcs resolve options

**--against** *ref*
: Merge this ref first, then settle what it conflicts with

### vcs checkpoint options

**--preserve**
: Also capture the uncommitted work and print a handle that restores it

## Subcommands

**ls-files**
: List the dirty paths a commit of this change wants; stages nothing

**resolve**
: Settle an in-progress merge's conflicted generated files, then regenerate once

**checkpoint**
: Print the working state's identity, for recording what a lease was handed; writes nothing unless --preserve

**merge-driver**
: The per-file merge driver git and hg invoke; you do not run this by hand

## Examples

*Stage the dirty sources and nothing else*

```sh
magus vcs ls-files -o name | git add --pathspec-from-file=-
```

*List one subtree's changes, with the outputs they regenerated*

```sh
magus vcs ls-files --include-generated 'internal/guard/**'
```

*List a new file too*

```sh
magus vcs ls-files --include-untracked --project libs/xdg
```

*Settle a conflicted merge*

```sh
magus vcs resolve
```

*Merge the base in and settle it in one step*

```sh
magus vcs resolve --against origin/main
```

*Record what a lease was handed*

```sh
magus vcs checkpoint
```

*The one citable token, for a ledger cell*

```sh
magus vcs checkpoint -o name
```

*Capture the uncommitted work too, before something risky*

```sh
magus vcs checkpoint --preserve
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-where**(1)](magus-where.md), [**magus-run**(1)](magus-run.md), [**magus-affected**(1)](magus-affected.md), [**magus-x**(1)](magus-x.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-query**(1)](magus-query.md), [**magus-refs**(1)](magus-refs.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-graph**(1)](magus-graph.md), [**magus-diff**(1)](magus-diff.md), [**magus-session**(1)](magus-session.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-queue**(1)](magus-queue.md), [**magus-gate**(1)](magus-gate.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-server**(1)](magus-server.md), [**magus-broker**(1)](magus-broker.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-status**(1)](magus-status.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-init**(1)](magus-init.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

