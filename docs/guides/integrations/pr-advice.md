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
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  with:
    doctor: 'false'
    blast-radius: 'false'
```

The token is never an input: the step sets `GITHUB_TOKEN` from a secret in its `env`,
and the action fails when it is missing.

Nor is the pull request. The action reads it from the event that started the workflow,
so run the step on `pull_request` (or `pull_request_review`, for `merge-queue`); an event
carrying no pull request, such as a push, leaves every advisor silent. A switch reads
`'true'` or `'false'`, and anything else fails the step. An advisor that fails is
reported, the rest still run, and the step fails at the end.

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
they are composed into one comment; locally they are a section of the `--impact` report,
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

**`first-contribution` does not run.** It asks the forge whether the pull request's author
has merged anything before, and a working tree has no author. Neither does `merge-queue`,
for the same reason: it reads the pull request's review state and labels.

## What each advisor says

| input                   | it comments when                                                                                                                                         |
| ----------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `merge-conflicts`       | the pull request conflicts with its base in files magus generates, which a merge driver cannot settle on the server                                      |
| `hand-edited-generated` | a generated file changed and nothing that produces it did, so the next regeneration overwrites the edit                                                  |
| `unclaimed`             | changed files belong to no project, so no target reads them and the checks say nothing about them                                                        |
| `target-outputs`        | a new target declares no outputs, which means it never replays from cache                                                                                |
| `skip-cache`            | a target opts out of the cache, quoting the reason magus requires for it                                                                                 |
| `blast-radius`          | the change reaches a large share of the workspace, with the chain that pulled each project in                                                            |
| `doctor`                | `magus doctor` reports a failing check; run it locally for the advice tier and its detail                                                                |
| `version-floor`         | the pull request raises `required_version`, which every contributor must act on                                                                          |
| `conformance`           | a new target, or a symbol the change adds, renames or re-signs, departs from what the rest of the workspace does with the same work or declaration       |
| `missing-target`        | the change adds a project, or drops a target, leaving it short of one its kind overwhelmingly has                                                        |
| `api-surface`           | the change touches symbols reachable outside the project that defines them, and with a `baseline`, what it did to each and the smallest bump that proves |
| `first-contribution`    | the author has no merged pull request here yet                                                                                                           |
| `merge-queue`           | off by default; the pull request is approved and could join `magus queue` but has not, naming the label and the methods the provider allows              |

`merge-queue` appears only while the pull request is open, approved, targets
`merge-queue-base` (the default branch unless set), comes from this repository, and
carries neither auto-merge nor a queue label. It reads the label's prefix and the allowed
merge methods from `magus queue describe --provider <merge-queue-provider>`, so it names
what the provider accepts, and it retracts once the pull request is queued, merged or
closed. Those states change on labels and reviews, so run it from a workflow triggered
by them, as this repository's `queue-advice.yaml` does:

```yaml
- uses: egladman/magus/.github/actions/advice@v0.4.0
  env:
    GITHUB_TOKEN: ${{ secrets.GITHUB_TOKEN }}
  with:
    merge-queue: 'true'
```

`blast-radius` takes a `fanout-share` (default `0.5`): the share of the workspace a
change must reach before it says anything. It is a share rather than a count because
five projects is most of a small workspace and a rounding error in a large one.

## Conformance on code

The symbol half of `conformance` reads `magus diff`'s `checks` on each symbol the change
adds, renames or re-signs. Every check derives its norm from the workspace's own symbol
index, needs `conformance-min-cohort` declarations agreeing at `conformance-min-share` or
more (the same two inputs as the target half, default 5 and 0.8), never counts the change's
own new names toward a norm, and states a fact with its counts rather than a rule:

| check             | it reports                                                                                                                  |
| ----------------- | --------------------------------------------------------------------------------------------------------------------------- |
| `naming-affix`    | a function or type missing the leading or trailing word pair most declarations of its shape that share a word with it carry |
| `name-collision`  | a name at a project's top level that a target, spell, op, charm or diagnostic already carries                               |
| `rename-leftover` | a renamed symbol whose old two-word-or-longer name still appears in the files that reference it                             |
| `param-order`     | a function taking two parameters in the reverse of the order most functions in its scope that take both do                  |

The checks read what every language's index holds (names, kinds, scopes, references) and,
where a language reports it, the declaration's shape: `param-order` needs a language whose
index renders parameter names (Go and TypeScript today).

Before the checks run, `magus diff` brings the symbol index of every project the change
touched up to date through that project's `scip` target, so a current index replays and a
stale one rebuilds only itself. When it cannot (the indexer is missing or fails, or cache
writes are off so nothing vouches for the rebuilt index), the review carries
[MGS7003](../../reference/codes/knowledge/MGS7003.md) in place of findings, and this section
says so and fails its step. It never reads as a change with nothing to report. The job running
the advisors therefore needs cache writes on (`MAGUS_CACHE_WRITE_ENABLED: 'true'`); without a
signing key that still publishes nothing to a shared cache.

A `baseline` sharpens this half as it sharpens `api-surface`. With one, what the change
adds, renames and re-signs is read from the two indexes. Without one it is read from the
change's own patch: a symbol is new when its definition line is an added line that no
removed line in the patch names, and a re-signed symbol is not compared at all. When the
workflow's baseline step fails, the section says so under its findings rather than quietly
reporting less.

## The bump is a floor

`api-surface` takes a `baseline`: a `magus graph export --symbols -o json` of the revision
the pull request started from. With one, it compares every changed symbol against its base
and reports what the change did to it - added, removed, re-signed, or changed in the body -
and the semver bump that evidence proves.

The bump is a LOWER bound and never a ceiling. A removed public symbol proves a major, an
added one proves a minor, and nothing a graph holds can prove a change is small: behavior
moves under an unchanged signature. So raise it freely and never lower it.

A changed signature is reported apart from the floor, as the likely bump. Signatures are
compared as the indexer rendered them, which is what keeps this language-agnostic and is
also the one place it can be wrong: renaming a parameter, widening a type, or changing a
constant's value all move the rendered text without breaking a consumer.

Index both sides in one checkout. Two environments render one declaration differently -
a missing `node_modules` turns a TypeScript parameter into `any` - and every difference
would read as a changed signature.

```sh
git worktree add /tmp/base "$(git merge-base HEAD origin/main)"
magus --root /tmp/base graph build
magus --root /tmp/base graph export --symbols -o json --tee /tmp/base.json -q > /dev/null
magus diff --baseline /tmp/base.json
```

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

The advisors run after your checks and never gate them, and none of them writes to the
pull request: the action needs `pull-requests: write` for its comment and nothing more.
Regenerated files reach the base branch through [`magus queue`](../../concepts/merge-queue.md),
which settles generated-file conflicts at merge time in a job that runs no pull request
code.

## See also

- [Git integration](git.md): what the `merge-conflicts` advisor is telling you to do, and
  why settling a stack takes merges rather than rebases.
- [CI integration](ci.md): wiring magus into a pipeline.
- [Cache model](../../concepts/cache.md): why a target without declared outputs never
  replays.
- [Affected](../../concepts/workspace/affected.md): what "reaches" means, and how
  magus computes it.
