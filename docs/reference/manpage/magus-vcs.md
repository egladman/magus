---
title: magus vcs
description: "Stage a change the way the workspace's declarations say it should be staged, and settle an in-progress merge's conflicted generated files by regenerating them once."
tags: [cli, magus vcs, git, merge, conflicts, generated files, staging]
---

# magus-vcs

Staging and conflict resolution that knows what is generated

## Synopsis

**magus** vcs \<add|resolve|merge-driver\> [flags]

## Description

Version-control operations that read the workspace's output declarations,
so generated files and hand-written sources are treated differently.

add stages what the workspace declares: sources, and the generated outputs a
source change in the same commit accounts for. Anything undeclared is reported
rather than swept in, which is the difference between it and git add -A.

resolve settles an in-progress merge, rebase, or cherry-pick. It classifies
every conflicted path at once, regenerates once instead of once per file,
settles the files one side deleted (which no VCS invokes a merge driver for),
and stages everything the regeneration touched so a following commit or
rebase --continue does not refuse on a dirty tree. Conflicts in files magus
does not generate are reported and left alone. Pass --against \<ref\> to merge
that ref first and settle what it conflicts with; add --dry-run to see the
classification and have the merge backed out again.

merge-driver is the per-file driver git and hg invoke during a merge. You do
not run it by hand; it is wired per clone, because a driver registration
cannot be committed. That is also why a forge reports conflicts your own
clone would settle silently, and why resolve exists as the bulk counterpart.

resolve works on git, Mercurial and Jujutsu. Only --against is git-only: merge the
base in yourself on the others, then run resolve.

### vcs add options

**--untracked**
: Also stage undeclared files

### vcs resolve options

**--against** *ref*
: Merge this \`ref\` first, then settle what it conflicts with

## Subcommands

**add**
: Stage a change the way this workspace's declarations say it should be staged

**resolve**
: Settle an in-progress merge's conflicted generated files, then regenerate once

**merge-driver**
: The per-file merge driver git and hg invoke; you do not run this by hand

## Examples

*Stage a change without sweeping in build residue*

```sh
magus vcs add
```

*Classify the dirty tree, stage nothing*

```sh
magus vcs add --dry-run
```

*Settle a conflicted merge*

```sh
magus vcs resolve
```

*Merge the base in and settle it in one step*

```sh
magus vcs resolve --against origin/main
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-hook**(1)](magus-hook.md), [**magus-notify**(1)](magus-notify.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

