---
title: Pull request advice
description: What magus comments on a pull request, why each finding exists, and how to turn any of it off - per advisor, per repository, or entirely.
tags: [ci, github, pull-request, advice, comments, review, doctor, cache]
---

# Pull request advice

magus can leave one comment on a pull request describing what your workspace's own
build graph noticed about the change. Each finding is a section of that single
comment, it rewrites itself on every push, and a section disappears the moment its
finding stops being true.

None of it blocks a merge. The checks do that.

The same advisors run at the keyboard - `magus diff --impact`, before you push. See
[Before the push](#before-the-push-the-same-advisors-locally).

## Turning it off

Every advisor is an input, and every input defaults to `true`. Set the one you do
not want to `false`:

```yaml
- uses: egladman/magus/.github/actions/advice@v0.4.0
  with:
    doctor: 'false'
    blast-radius: 'false'
```

To stop the comment entirely, drop the step. If you would rather keep the step and
switch it off without editing the workflow, the job in magus's own `ci.yaml` is
guarded by a repository variable, which you can copy:

```yaml
if: github.event_name == 'pull_request' && vars.MAGUS_PR_COMMENTS != 'false'
```

Set `MAGUS_PR_COMMENTS` to `false` under Settings, Variables, and the job stops
running without a commit.

## Silencing one finding on one pull request

Turning an input off is a decision about the repository. Silencing is usually a decision
about the change in front of you: you looked, it does not apply here, and you want it to
stop without committing that opinion to everyone forever.

Label the pull request:

| label                     | effect                                   |
| ------------------------- | ---------------------------------------- |
| `magus:silence`           | mutes every advisor on this pull request |
| `magus:silence:unclaimed` | mutes one, by its input name             |

A silenced advisor retracts its section rather than freezing the last thing it said, so
the comment never shows a finding nobody is still checking. Remove the label and it comes
back on the next push.

The label is visible on the pull request, which is the point: the next reader can see
what was muted and by whom.

## Before the push: the same advisors, locally

The advisors are not pull-request-only. `magus diff --impact` runs the read-only ones
against your working tree, in process, before you push:

```sh
magus diff --impact
```

Same scripts, same graph, same wording - the difference is where the answers go. In CI
they are composed into one comment; locally they are a section of the preflight report,
where acting on a finding still costs one edit rather than a review round trip.

Four things differ, all deliberate:

**Labels do not apply.** `magus:silence` is a fact about a pull request, and a local run
has none. Nothing is silenced at the keyboard, on purpose: you asked for the report, so
you want everything in it. Silencing is a decision to record where the next reader can
see it, which is the pull request.

**The base is whatever your clone has.** A local run never fetches. `magus diff` is a
read-only report, it may run offline, and under `--watch` it re-fires on every save -
none of which may write `refs/remotes/`. So it compares against the `origin/<base>` in
your clone as it stands, and the report says how old that is:

```text
BASE: origin/main, tip 6 days old - a local run stays off the network, so anything
merged since is outside what the advisors saw; `git fetch origin main` brings it forward
```

This is the one place local and CI legitimately disagree. CI fetches the base before it
compares, so a finding that appears in the comment and not on your machine usually means
your `origin/main` is behind, not that the advisor changed its mind. Fetch and rerun
before you go looking for a bug.

**Uncommitted edits count.** CI diffs the pull request's commits, `base...head`. A local
run diffs the working tree against the merge base, so the change it describes is the one
you are about to commit rather than the one you already did. Committed work is still
included - the merge base is the same starting point either way. Untracked files are
outside both: `git diff` reports tracked paths, so a brand new file no project claims is
invisible until you `git add` it.

**`first-contribution` does not run.** It asks the forge who opened the pull request,
which is not a question a working tree can answer.

## What each advisor says

| input                   | it comments when                                                                                                    |
| ----------------------- | ------------------------------------------------------------------------------------------------------------------- |
| `merge-conflicts`       | the pull request conflicts with its base in files magus generates, which a merge driver cannot settle on the server |
| `hand-edited-generated` | a generated file changed and nothing that produces it did, so the next regeneration overwrites the edit             |
| `unclaimed`             | changed files belong to no project, so no target reads them and the checks say nothing about them                   |
| `target-outputs`        | a new target declares no outputs, which means it never replays from cache                                           |
| `skip-cache`            | a target opts out of the cache, quoting the reason magus requires for it                                            |
| `blast-radius`          | the change reaches a large share of the workspace, with the chain that pulled each project in                       |
| `doctor`                | `magus doctor` reports a failing check; run it locally for the advice tier and its detail                           |
| `version-floor`         | the pull request raises `required_version`, which every contributor must act on                                     |
| `conformance`           | a new target's name diverges from what the rest of the workspace already calls the same work                        |
| `missing-target`        | the change declares a project, or drops a target, leaving it short of one its kind overwhelmingly has               |
| `api-surface`           | the change touches symbols reachable outside the project that defines them, which is what a version bump is about   |
| `first-contribution`    | the author has no merged pull request here yet                                                                      |
| `fix-generated-drift`   | off by default; regenerates drifted files and pushes them, and only with a label                                    |
| `fix-merge-conflict`    | off by default; merges the base in, settles conflicts in generated files by regenerating, and pushes                |

`blast-radius` takes a `fanout-share` (default `0.5`): the share of the workspace a
change must reach before it says anything. It is a share rather than a count because
five projects is most of a small workspace and a rounding error in a large one.

## Why a single comment

A comment per finding turns a pull request into a wall of bot noise, and a thread
that only grows is one people mute. One comment with a section per advisor means one
notification, one place to look, and a body that tells you what is true right now
rather than what was true three pushes ago.

Each section is collapsed, and its summary line carries the count, so the comment
stays a table of contents until you open something.

## When it says nothing

A pull request with no findings gets **no comment at all**. The first advisor with
something to say opens the comment, so a clean change carries no trace that magus looked.
There is no "nothing found" placeholder: open one five times for nothing and you stop
opening it, including the time it has a finding. Your checks list already reports that
the run happened.

Once you settle the last finding, magus rewrites the comment to say so and leaves it in
place. That keeps the thread and its replies, and it tells whoever reads the merged pull
request that magus raised something and that someone handled it.

Every advisor answers one question: what did **this** pull request do? A finding that
holds on the base branch is backlog, and you work through backlog with `magus doctor` at
the keyboard.

## What it costs

The advisors run after your checks and never gate them. `fix-generated-drift` is the
only one that writes anything, and it needs both the input and a label on the pull
request before it will.

## See also

- [Git integration](git.md): what the `merge-conflicts` advisor is telling you to do, and
  why settling a stack takes merges rather than rebases.
- [CI integration](ci.md): wiring magus into a pipeline.
- [Cache model](../../concepts/cache.md): why a target without declared outputs never
  replays.
- [Affected](../../concepts/workspace/affected.md): what "reaches" means, and how
  magus computes it.
