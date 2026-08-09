---
title: Git integration
description: What magus writes into your repository - the generated-file merge driver, the .gitattributes section behind it, and the refresh hooks - plus the rule every magus hook obeys, which is that a hook hands off work and never does work.
tags: [git, vcs, hooks, gitattributes, merge-driver, generated-files, conflicts]
aliases: [guides/git]
---

# Git integration

magus writes three things into a repository, and no more:

- a managed section in `.gitattributes`, marking every declared output as generated
  and routing it to magus's merge driver;
- a `merge.magus.driver` registration in the clone's own git config, because a driver
  cannot be committed;
- the refresh hooks below, when the daemon starts.

All three are managed sections or single config keys. Nothing rewrites your history,
your branches, or a hook body you wrote yourself.

## Hooks hand off work, never do work

This is the rule, and it is not a performance target to aim at later. It decides
whether a hook may exist at all.

A git hook runs on the critical path of a command the user did not ask magus to be part
of. Someone typed `git checkout`. They are waiting on git, not on a build tool, and
every millisecond a hook spends is stolen from an operation that had nothing to do with
us. So the bar is not "fast enough". The bar is:

> A magus hook may look up something already computed, or hand the work to something
> else and return. It may not do the work.

That is checkable by reading the hook, not by timing it. A hook that computes is
disqualified even when it happens to be quick today, because the thing it calls will get
slower and nobody will notice until it does.

**Automatically disqualified**, whatever the measured time:

- starting a language runtime to answer the question (a cold `node`, `python`, or `deno`
  process has lost before it has parsed anything);
- loading the workspace, evaluating magusfiles, or opening the knowledge graph;
- touching the network, including a cache the hook thinks is nearby;
- running a build, a test, a linter, a formatter, or a generator;
- enforcing policy locally - blocking a commit, rewriting a file, staging something on
  your behalf.

That last one is worth stating plainly, because it is the one every other tool reaches
for. magus does not enforce in a hook. Enforcement belongs where it can be read,
re-run, and argued with: a target, a gate, or a CI check. A hook that blocks is a hook
people disable with `--no-verify`, and a disabled hook enforces nothing at all.

**Every magus hook also fails open.** The installed body ends in `|| true` and sends
its output to `/dev/null`, so a hook can never fail your git command, and a broken or
half-installed magus is invisible to it. A hook that is not certain it is correct does
nothing and says nothing.

## The hooks magus installs

`magus server start` installs them, best-effort: a repository with no daemon never gets
them, and a failure to write one is a warning, never a reason the daemon does not start.

| Hook            | Fires on                                                         |
| --------------- | ---------------------------------------------------------------- |
| `post-checkout` | a branch switch (guarded so a file checkout does not trigger it) |
| `post-merge`    | a merge or pull                                                  |
| `post-rewrite`  | a rebase or amend                                                |

Each is a history change that can stale the knowledge graph and the symbol index.
Mercurial gets the same treatment through its `update` hook; jj has no hook support and
gets none.

The body is one line:

```sh
magus server job sync-graph >/dev/null 2>&1 || true
```

That command is the whole point of the rule above. It does not reconcile anything. It
enqueues a job on the already-running daemon and returns, so the reconciliation happens
in the background, after your `git checkout` has finished, on a process that was going
to be running anyway. The hook's own cost is one short-lived client that posts a message.

They are written into a managed section:

```sh
# BEGIN magus-refresh - do not edit this section manually
magus server job sync-graph >/dev/null 2>&1 || true
# END magus-refresh
```

Your own hook body, above or below that section, is preserved across every reinstall.
Delete the section to remove the integration; the next `magus server start` puts it back.

## The merge driver, and what it cannot do

Generated files conflict constantly and merge meaninglessly: the correct merge of build
output is whatever regenerating produces, not a reconciliation of two byte streams. So
each declared output glob gets a `.gitattributes` entry:

```gitattributes
MAGUS.md merge=magus linguist-generated
```

`merge=magus` routes conflicts to `magus vcs merge-driver`, which git invokes per file.
You never run that command yourself. `linguist-generated` is the other half, and it is
the half that always works: it collapses the file in GitHub's diff view and keeps it out
of language statistics.

**A forge never runs a custom merge driver.** `merge=magus` needs `merge.magus.driver` in
a git config, which is per-clone and cannot be committed, so github.com computes
mergeability with the plain three-way merge and reports conflicts your local git would
have settled silently. The same is true of GitLab, Gerrit, and every merge queue. This
is architectural, not a configuration gap, and no `.gitattributes` change will fix the
conflict banner on a pull request.

A driver has a second limit: no VCS invokes a content merge driver when one side deleted
the file, so a modify/delete conflict reaches you untouched no matter how it is
configured.

Both are why the real answer is a bulk command rather than a per-file callback:

```sh
git merge origin/main
magus vcs resolve
```

`magus vcs resolve` classifies every conflicted path at once, regenerates once instead of
once per file, settles the deletions a driver is never called for, and stages everything
the regeneration touched so `git rebase --continue` does not refuse on a dirty tree.
Conflicts in files magus does not generate are reported and left for you.

To settle a branch against its base without merging first, hand it the ref:

```sh
magus vcs resolve --against origin/main
```

That merges, resolves, and leaves the merge in progress for you to commit. It needs a
clean tree, so backing the merge out cannot lose uncommitted work. Add `--dry-run` to see
the classification and have the merge backed out again.

## Staging

`magus vcs add` stages what the workspace declares: sources, and the generated outputs a
source change in the same commit accounts for. Anything undeclared is reported rather
than swept in, which is the difference between it and `git add -A`.

It also skips generated output that nothing in the change accounts for - output that moved
with no declared input behind it, which means either a different magus build produced it
or a generator is not deterministic. Name such a path explicitly to stage it anyway.
