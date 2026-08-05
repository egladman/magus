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

| label | effect |
| --- | --- |
| `magus:silence` | mutes every advisor on this pull request |
| `magus:silence:unclaimed` | mutes one, by its input name |

A silenced advisor retracts its section rather than freezing the last thing it said, so
the comment never shows a finding nobody is still checking. Remove the label and it comes
back on the next push.

The label is visible on the pull request, which is the point: the next reader can see
what was muted and by whom.

## What each advisor says

| input | it comments when |
| --- | --- |
| `merge-conflicts` | the pull request conflicts with its base in files magus generates, which a merge driver cannot settle on the server |
| `hand-edited-generated` | a generated file changed and nothing that produces it did, so the next regeneration overwrites the edit |
| `unclaimed` | changed files belong to no project, so no target reads them and the checks say nothing about them |
| `target-outputs` | a new target declares no outputs, which means it never replays from cache |
| `skip-cache` | a target opts out of the cache, quoting the reason magus requires for it |
| `blast-radius` | the change reaches a large share of the workspace, with the chain that pulled each project in |
| `doctor` | `magus doctor` reports a failing or advisory check |
| `version-floor` | the pull request raises `required_version`, which every contributor must act on |
| `first-contribution` | the author has no merged pull request here yet |
| `fix-generated-drift` | off by default; regenerates drifted files and pushes them, and only with a label |

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

## What it costs

The advisors run after your checks and never gate them. `fix-generated-drift` is the
only one that writes anything, and it needs both the input and a label on the pull
request before it will.

## See also

- [CI integration](ci.md): wiring magus into a pipeline.
- [Cache model](../../concepts/cache.md): why a target without declared outputs never
  replays.
- [Affected](../../concepts/workspace/affected.md): what "reaches" means, and how
  magus computes it.
