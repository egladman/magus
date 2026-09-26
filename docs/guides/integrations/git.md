---
title: Git integration
description: What magus writes into your repository - the generated-file merge driver, the .gitattributes section behind it, and the refresh hooks - plus the rule every magus hook obeys, which is that a hook hands off work and never does work, and how to settle a conflict in generated output without a rebase, a force-push, or a broken stack.
tags: [git, vcs, hooks, gitattributes, merge-driver, generated-files, conflicts]
aliases: [guides/git]
---

# Git integration

magus writes three things into a repository, and no more:

- a managed section in `.gitattributes`, marking every declared output as generated
  and routing it, and every file auto-resolution may settle, to magus's merge driver;
- a `merge.magus.driver` registration in the clone's own git config, because a driver
  cannot be committed, and, when `magus init --vcs git` wires it, the settle hooks that
  finish what the driver starts (see
  [Regeneration after the merge](#regeneration-after-the-merge));
- the refresh and drift-notice hooks below, when the server starts.

All of them are managed sections or single config keys. Nothing rewrites your history,
your branches, or a hook body you wrote yourself. Hooks your workspace writes in Buzz
are a fourth thing, installed only when you ask; see
[Your own hooks, in Buzz](#your-own-hooks-in-buzz).

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

One set of hooks is the exception to both rules, on purpose: the settle hooks the merge
driver's registration writes. They run a generator, stage on your behalf, and speak up
when they cannot. The rule above exists because a hook runs on a command that had
nothing to do with magus, and a merge is the one command that does: the driver has
already taken a side of a generated file inside it, on the promise that regeneration
follows, and a hook that hands that promise off to a server that may not be running
keeps it only sometimes. So those hooks keep it themselves, once, bounded to what the
merge changed, and fire for nothing that is not a merge. The details are in
[Regeneration after the merge](#regeneration-after-the-merge).

## The hooks magus installs

`magus server start` installs them, best-effort: a repository with no server never gets
them, and a failure to write one is a warning, never a reason the server does not start.

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
enqueues a job on the already-running server and returns, so the reconciliation happens
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

## The drift notice

`post-commit` and `pre-push` carry a second managed section, `magus-drift`, in the same
shape: post a `check-drift` job, return. The server compares the commit's changed sources
against the outputs they produce and runs `gofmt -l` over its changed, format-governed
files, then prints the fix and the command that folds it into the offending commit
(MGS4006 stale output, MGS4009 stale formatting).

It never blocks and never writes to your tree. CI is the check; this is the earlier
warning. A hook only runs where someone installed it and did not pass `--no-verify`, and
jj has no hooks at all, so hooks catch three backends on a good day and CI catches
everything. See [the gate is a commit hook, so it is not a gate](../../concepts/targets/ci.md).

The difference is the feedback loop: CI rejects the same commit ten minutes later on a
pull request you then have to push again; the notice catches it while it is still the
commit in front of you.

## Your own hooks, in Buzz

The rule above binds the hooks magus installs for itself. A hook your workspace writes is
your policy, and whether it blocks is your call. What magus adds is a way to keep it under
version control, written in the same language as your magusfile, instead of a script in
`.git/hooks` that nobody reviews and no clone receives.

`spells/git/hooks.buzz` in the magus repository is a workspace-local Buzz module; copy it
into your workspace's `spells/git/`. It takes a directory of `<hook>.buzz` files, one per
[git hook](https://git-scm.com/docs/githooks), and installs a short sh shim for each into
the directory git runs hooks from. That is `core.hooksPath` when it is set, and otherwise
the common directory's `hooks/`, which linked worktrees share. The shim runs the
checkout's own copy with `magus buzz -s <file> -- <git's arguments>`, so each worktree
runs its own version of the hook, and a non-zero exit from `main` blocks the git operation.

Wire it to two targets:

```buzz
import "spells/git/hooks" as githooks;

export fun git_hooks_install(ctx: magus\Context, args: [str]) > void !> any {
    githooks\install(ctx, dir: "tools/git-hooks");
}

export fun git_hooks_remove(ctx: magus\Context, args: [str]) > void !> any {
    githooks\remove(ctx, dir: "tools/git-hooks");
}
```

Then write the hook, a Buzz file whose `main` takes git's arguments:

```buzz
import "fs";
import "os";

fun main(args: [str]) > void !> any {
    if (fs\readFile(args[0]).trim() == "") { os\exit(1); }
}
```

Save it as `tools/git-hooks/commit-msg.buzz` and run `magus run git-hooks-install:rw .`.
Adding another hook later is the same two steps: the file, then the target.

Both targets follow the [`rw` charm](../../concepts/charms.md#the-rw-charm): without it they
report what they would change and fail, and with it they change it. `--dry-run` writes
nothing. The rules they enforce:

- A file named for no git hook is an error, not a hook git would silently never run.
- An existing hook install did not write is an error. Delete it, or call your Buzz file
  from it.
- `remove` deletes only shims install wrote, found by the marker on their second line,
  including a shim whose Buzz file you have since deleted.
- The shim exits 0 with a notice when magus is not on `PATH`, and silently when the
  checked-out branch has no copy of the Buzz file.
- The shim sets `BUZZ_INCLUDE_PATH` to the project directory, so a hook imports workspace
  Buzz by its project-relative path.

The magus repository's own
[`commit-msg.buzz`](https://github.com/egladman/magus/blob/main/tools/git-hooks/commit-msg.buzz)
is the worked example: it refuses a commit made directly on the base branch whose subject
is not a conventional commit, by the rule CI applies to pull request titles.

Two limits worth knowing. A hook runs only where someone installed it and did not pass
`--no-verify`, so anything that must hold belongs in CI as well. And `magus server start`
writes managed sections into `post-checkout`, `post-merge`, `post-rewrite`, `post-commit`
and `pre-push`, and the merge driver's registration into `pre-merge-commit`,
`pre-commit`, `post-commit`, `post-rewrite` and `post-applypatch`; install refuses those
files as hooks it did not write, so a Buzz hook for one of those names needs the managed
section removed first.

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

### Diff drivers

The same managed block opens with a diff driver for each source language magus reads:

```gitattributes
*.go diff=golang
*.ts diff=typescript
*.buzz diff=buzz
```

A diff driver's hunk-header pattern is how git names the declaration a change lands in,
the text after the second `@@` of each hunk. A job's footprint reads those names to say
which functions, types and tests a change touched, and `git diff` prints them for people
too. `golang`, `python`, `rust` and `markdown` ship with git. `typescript` and `buzz` do
not, so magus registers their patterns as `diff.typescript.xfuncname` and
`diff.buzz.xfuncname` in git config, beside `merge.magus.driver` and in the same scope.
Each attribute resolves to the last line that sets it, and a driver line sets only `diff`,
so a generated `.go` file keeps `merge=magus` and gains `diff=golang`. A workspace load
restores a missing line or registration, and `magus doctor` fails its
`merge-driver-loads-workspace` check when one is still missing.

### Auto-resolving source files

A source file conflicts for real more often than not, but some conflict in ways nobody
has to think about: two branches each appending an entry to `CHANGELOG.md`. The driver
settles such a file when two things hold, the same two the
[merge queue](../../concepts/merge-queue.md#auto-resolving-source-conflicts) checks:

- the merge settles. Each side's edits are hunks placed by the file's diff driver, and a
  region both sides changed settles only when both made the same change, one side's
  change holds the other's, or both only added lines where the base had none (ours
  first, then theirs).
- the change is low risk by magus's one change classifier, the one gate sizing tiers from: generated,
  prose (`gate_low_risk`, markdown by default) or comment-only. Code qualifies only where
  its project lists it in `merge_low_risk`.

Anything else leaves the whole file conflicted, with conflict markers the driver writes
itself (git keeps whatever a failed driver left in the file, so without them the other
side's change would vanish), and a message naming each region that did not settle as
`path#declaration`, or the classifier's line. A file both sides added, a binary file and
a symlink are never settled.

Every declared `gate_low_risk` and `merge_low_risk` glob gets its own line in the managed
section, `merge=magus` without `linguist-generated`, since the file is source and review
has to show it. A glob with no slash is written anchored, because git would otherwise
match it at any depth. The built-in markdown defaults are not written, so a workspace
that declares neither key keeps the `.gitattributes` it had: declare
`"gate_low_risk": ["**/*.md"]` to route markdown to the driver. A comment-only edit has
no glob either, so git and hg reach one only through the queue or `magus vcs resolve`.

Each VCS reaches the driver its own way, and the decision is the driver's in all of them:

| VCS                | Registration                                         | When it runs                                                      |
| ------------------ | ---------------------------------------------------- | ----------------------------------------------------------------- |
| git                | `.gitattributes` and `merge.magus.driver`            | during `git merge`, `rebase` and `cherry-pick`                    |
| Mercurial, Sapling | `[merge-patterns]` and `[merge-tools]` in the config | during `hg merge` and `sl` merges                                 |
| Jujutsu            | `merge-tools.magus` in the repository's config       | `magus vcs resolve`, or `jj resolve --tool magus <files>` by hand |

jj records a conflict in the commit and never runs a tool on its own, so `magus vcs
resolve` runs `jj resolve --tool magus` over each conflicted source file before it
settles the rest, and the driver decides each, comment-only code included. The registration (`jj config set --repo`) leaves `ui.merge-editor`
alone, and tells jj that exit status 1 means the file still conflicts, so jj keeps its
own markers for a file the driver does not settle.

### Regeneration after the merge

The driver keeps the current version of the file and does not regenerate it. git calls
it once per conflicted file while the merge is still writing the tree, so a generator
started there reads a half-merged checkout, and what it writes dirties the tree against
what git has staged, which is how the first version of this driver stopped every
`git rebase --continue` dead. Instead the driver records what it owes: the project, the
target that rebuilds the file, and the file itself, in `magus-owed-regeneration.json` in
the worktree's git directory. Fifty kept files of one target are one entry.

The regeneration itself happens in the settle hooks, which `magus init --vcs git`
writes beside the driver: a managed section `magus-regenerate` in `pre-merge-commit`,
`pre-commit`, `post-commit`, `post-rewrite` and `post-applypatch`, each running
`magus vcs resolve --hook <name>`. A workspace load refreshes the driver's
registration and deliberately not the hooks: the hooks dir is shared by every worktree
of the repository, and a hook that runs a generator is written once, on request, not
by whichever worktree's magus loaded last. `magus doctor` reports a registered driver
whose hooks are missing under `settle-hooks`. Each hook fires once the tree is whole, and the hook
regenerates more than the driver kept: every project whose declared sources or outputs
the operation changed runs its `generate` target, deepest projects first, so `docs`
regenerates before the root that indexes its pages. A clean merge that brings in a
change to a generator's input regenerates its output too, driver or no driver. An
operation that touches no generator's input runs nothing and prints nothing.

What happens to the regenerated output follows the hook, because git's hooks differ in
whether the commit exists yet:

| Operation                                                                        | Hook               | Where the output lands                                                                                                                                        |
| -------------------------------------------------------------------------------- | ------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `git merge`, `git pull` (clean)                                                  | `pre-merge-commit` | Staged into the merge, and git's own commit is stopped: "use 'git commit' to complete the merge". Your `git commit` is the merge commit, already regenerated. |
| a merge, cherry-pick or revert git stopped on, then `git commit` or `--continue` | `pre-commit`       | Staged into that commit.                                                                                                                                      |
| `git cherry-pick`, `git revert` (clean)                                          | `post-commit`      | Staged after the commit; the line prints `git commit --amend --no-edit`.                                                                                      |
| `git rebase`, `git pull --rebase`, `--continue`                                  | `post-rewrite`     | Staged after the rebase, once for the whole rebase; the line prints the amend.                                                                                |
| `git am`                                                                         | `post-applypatch`  | Staged after the last patch of the series; the line prints the amend.                                                                                         |

The clean-merge row is the deliberate one. git computes a merge commit's tree before it
runs `pre-merge-commit`, so output staged there would land in your index and not in the
commit. Stopping the commit is git's own `--no-commit` shape: nothing is rewritten, no
stale merge commit is ever written, and the `git commit` you run next carries the
regenerated output without running the generator again. magus never amends; after a
rebase or a clean pick the commits exist, so it stages, tells you, and leaves the amend
to you.

The hooks are quiet where they do not apply. `pre-commit` and `post-commit` read the git
directory from the shell and start magus only under `MERGE_HEAD`, `CHERRY_PICK_HEAD` or
`REVERT_HEAD`; a pick inside a rebase waits for `post-rewrite`; a cherry-pick with picks
still to come is left alone, since staging would make the next pick refuse, and the last
pick settles the lot. Two cases are missed: a clean single `git revert`, which writes no
`REVERT_HEAD`, and `git merge --squash`, which makes no merge commit. Both are ordinary
commits to the hooks; the drift notice catches them.

Failure is loud. A regeneration that fails, or a workspace that cannot load mid-merge,
prints why and the exact `magus run generate:rw ...` to run; in `pre-merge-commit` and
`pre-commit` it also stops the commit, so no commit carries output magus knows is stale
(`git commit --no-verify` commits anyway, and says so). The run stays owed in the git
directory, `magus doctor` reports it under `owed-regeneration`, and `magus vcs resolve`
with nothing conflicted runs what is owed, stages it and clears the record. The one
failure that does not stop a commit is magus itself being absent: the hooks dir is
shared by every worktree of a repository, each with a `./magus` of its own, so a hook
that finds no binary, or one that predates `vcs resolve --hook`, prints a line saying
the operation was not regenerated and lets the commit through.

Only git has these hooks. Under Mercurial, Sapling and jj the driver logs the command to
run before committing, and `magus vcs resolve` runs it. The merge queue never runs them
either: it merges trees without a checkout and regenerates each candidate through its own
hook, and the registration refresh a queue candidate cannot write is skipped, not
reported.

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
Conflicts in files magus does not generate are reported and left for you, after the
driver has had its turn at them.

To settle a branch against its base without merging first, hand it the ref:

```sh
magus vcs resolve --against origin/main
```

That merges, resolves, and leaves the merge in progress for you to commit. It needs a
clean tree, so backing the merge out cannot lose uncommitted work. Add `--dry-run` to see
the classification and have the merge backed out again.

`magus vcs resolve` works on git, Mercurial and Jujutsu - all three implement conflict
reporting. Only `--against` is git-only, because only git has a merge-starting implementation;
on the others, merge the base in yourself and then run `magus vcs resolve`.

## Merge, do not rebase

Reach for `git merge origin/main`, not `git rebase origin/main`. This is not a style
preference. The rebase costs you three things the merge does not:

- **A force-push.** A rebase rewrites your commits, so the branch can only move with
  `--force-with-lease`. A merge only adds a commit, so a plain `git push` works.
- **Everything stacked on you.** A force-push moves the commits every dependent branch was
  built on, so each branch above yours needs the same treatment, in order, every time the
  base moves. A merge leaves them all valid.
- **The conflict, repeatedly.** A rebase replays each of your commits against the new base,
  so a generated file can conflict once per commit. A merge settles it once.

The usual reason to prefer a rebase is a linear history on the trunk, and a squash merge
already gives you that: it collapses the branch to a single commit, so merge commits inside
your branch never reach the trunk at all. If your project squash-merges, merging the base
into your own branch is free.

## Stacked pull requests

A stack is a chain of branches, each based on the one below it. Generated files are the
usual reason one falls apart, and the mechanism is worth knowing.

When the bottom pull request is squash-merged, the trunk gains a **new** commit that is not
an ancestor of the branch above, while that branch still carries its own copy of the commits
that were just squashed. For hand-written files git copes: both sides made the same change,
and a three-way merge recognizes that. For generated output it cannot. The trunk's output was
regenerated from the bottom branch's sources alone, and the branch above holds output
regenerated from both, so both sides changed the same region differently. That is a real
conflict, in a file neither author edited.

Settle a stack from the bottom up, one branch at a time, each against its own base:

```sh
git switch feature-b          # the branch directly above what just merged
git merge origin/main
magus vcs resolve
git push

git switch feature-c          # then the next one up, against ITS base
git merge origin/feature-b
magus vcs resolve
git push
```

Every step is a merge, so nothing is force-pushed and settling one layer does not invalidate
the layers above it. The same walk with rebases means redoing every branch above whichever
one you touch.

## Whose conflict is it

`magus vcs resolve` reports the paths it deliberately left alone. To answer the same question
before you start a merge, ask the workspace - `magus describe file` reads the declarations
rather than guessing from a path convention:

```sh
magus describe file MAGUS.md internal/describe/extract.go
```

`role: output` is generated: regenerate it, never hand-merge it, and do not bother reading
its diff. `role: source` is yours, and an ordinary conflict.

## When the driver is doing nothing

The driver's failure mode is quiet, and it looks like the opposite of a failure - git reports
a conflict in a generated file it should have settled by itself. A file that both sides
changed to the same bytes is the clearest tell, because that is the easiest merge there is.

The registration is a command line, held in one clone's config:

```sh
git config --get merge.magus.driver
```

It names a magus binary and a subcommand. If that command cannot run - the binary moved, or
it predates the subcommand the registration names - the driver exits non-zero, and git reads
a non-zero driver as _conflict_. Every file routed to it is then reported as conflicted
whether it is or not, which inflates the count rather than raising an error.

magus rewrites the registration whenever it opens a workspace, so running a command that
loads one repairs it; `magus ls` is enough. Two things stop that happening on their own: a
magus that cannot load this workspace never reaches the refresh, and the `vcs` verbs skip it
on purpose, because the refresh writes the tracked `.gitattributes` and those verbs run while
that file may itself be unmerged.

`magus vcs resolve` does not depend on the driver - it reads the conflicted paths from git
and regenerates - so it settles the inflated list too. A broken driver makes a conflict look
worse than it is; it does not stop you fixing it.

## Staging

`magus vcs add` stages what the workspace declares: sources, and the generated outputs a
source change in the same commit accounts for. Anything undeclared is reported rather
than swept in, which is the difference between it and `git add -A`.

It also skips generated output that nothing in the change accounts for - output that moved
with no declared input behind it, which means either a different magus build produced it
or a generator is not deterministic. Name such a path explicitly to stage it anyway.

## Reading a change through git

`magus diff` reads a changeset the way this repository's conventions rank it: declared
outputs folded away, the rest ordered by what they can break. Nothing about that
requires leaving git.
[Reviewing your changes](../reviewing-changes.md) covers what it reports; this section is
only how to reach it without typing a magus command.

Wire it as git's pager for `diff`, and plain `git diff` renders through magus:

```sh
git config pager.diff 'magus diff -'
```

That is the whole integration. `git diff`, `git diff <ref>`, and `git diff --staged`
all work, because git hands its pager the entire patch on stdin and `magus diff -`
reads a patch on stdin. Nothing is intercepted that you cannot get back:
`git --no-pager diff` prints the raw patch, and `git -c pager.diff=cat diff` does the
same for one invocation.

Prefer to opt in per command rather than always? An alias costs one line and leaves
`git diff` alone:

```sh
git config alias.reading '!f(){ git diff "$@" | magus diff -; }; f'
```

Then `git reading` and `git reading main` read through magus, and `git diff` does not.

### Why not an external diff or a difftool

`GIT_EXTERNAL_DIFF` and `diff.external` are for a program that renders ONE file's diff:
git calls them once per file, with seven arguments. Almost everything magus has to say
is a property of the whole changeset - which projects rebuild, who owns them, what it
costs, what to read first - so per-file invocation would mean printing the report once
per file, or not at all. Wire magus there and it refuses, naming the pager setting
above; it does not half-answer.

`git difftool --dir-diff` has the opposite problem. It copies the changeset into two
temporary directories and runs the tool against those. magus refuses to run inside a
temporary copy of a tree on purpose: the verdict would describe a workspace nobody
ships, and anything regenerated would land in the copy. Per-file `difftool` is the
external-diff mismatch again, with a prompt between each file.

The pager is the one git integration point whose contract already matches: the whole
diff, once, on stdin.

### The same shape in the other backends

This is not a git quirk. Every backend magus supports offers a diff-tool slot and a
pager, and in each of them the tool slot is the wrong shape for the same reason -
measured against the installed versions:

| backend   | diff-tool slot hands the tool                                                                              | pager hands the tool                  |
| --------- | ---------------------------------------------------------------------------------------------------------- | ------------------------------------- |
| git       | seven arguments, once per file (`GIT_EXTERNAL_DIFF`), or two temp directories (`difftool --dir-diff`)      | the whole unified diff on stdin       |
| Mercurial | two directories - a temp snapshot and the working dir (`extdiff`)                                          | the whole unified diff on stdin       |
| Sapling   | two file paths, once per file (`extdiff`)                                                                  | the whole unified diff on stdin       |
| Jujutsu   | two directories, `$left` and `$right` (`ui.diff-formatter`, or `file-by-file` with `diff-invocation-mode`) | whatever `ui.diff-formatter` produced |

So the pager is the portable answer, and two settings make it work everywhere:

```sh
hg config --edit    # [pager] pager = magus diff -   and   [color] mode = off
sl config --user pager.pager 'magus diff -'
jj config set --repo ui.diff-formatter ':git'
jj config set --repo ui.pager '["magus", "diff", "-"]'
```

Jujutsu needs the extra line because its default diff is a side-by-side rendering
rather than a patch; `:git` makes `jj diff` emit the unified form its pager then hands
over. With that set, jj behaves like the rest.

**Turn color off for the diff being handed over.** A VCS colorizes when it believes it
is writing to a terminal, and paging is exactly that case. A colorized patch has escape
sequences in front of every header, so the headers no longer begin a line and nothing
parses. magus refuses such a patch and names this as the cause rather than reporting an
empty changeset, but the fix is upstream: `--color=never`, `hg --config color.mode=off`,
or `jj --config ui.color=never`. git does not colorize into a pager by default and needs
nothing.

Every temp-directory variant above is refused for one reason: magus declines to run
against a copy of a tree, because the verdict would describe a workspace nobody ships
and anything regenerated would land in the copy.

### Reading a patch you did not produce

The same input works from anywhere a patch comes from - a colleague, a mail
attachment, a stash, a code-review tool:

```sh
gh pr diff 123 | magus diff -
```

Both patch dialects parse: git's `diff --git a/x b/x` headers, and the bare
`--- a/x` / `+++ b/x` pair that GNU `diff -u` and `patch` speak. A patch magus cannot
read is refused rather than reported as an empty changeset.
