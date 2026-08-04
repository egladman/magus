---
title: CI checkout
description: How to check out a repository for magus on any CI provider - why a blobless partial clone is the right default, why fetch-depth 0 gets more expensive every day the repository lives, and how magus deepens a shallow clone by itself rather than silently rebuilding the world.
tags:
  [
    ci,
    checkout,
    clone,
    partial clone,
    blobless,
    fetch-depth,
    shallow,
    affected,
    merge base,
    MGS1010,
    github actions,
    gitlab,
    circleci,
    buildkite,
    jenkins,
    azure pipelines,
  ]
order: 3
---

# CI checkout

Every incremental-CI tool tells you to clone the full history, and then never
mentions it again. It is the single line in a pipeline whose cost grows every day
the repository lives, and it buys almost nothing. This page is the model behind a
cheaper checkout and the per-provider recipes that implement it.

## What magus actually needs

[`magus affected`](../../concepts/workspace/affected.md) answers one question: which
files differ between the base branch and this working tree. For git that is three
commands:

```sh
git merge-base origin/main HEAD
git diff --name-only <merge-base>
git ls-files --others --exclude-standard
```

Read what those need. `merge-base` walks **commits**. `diff --name-only` compares
**tree** entry object IDs on either side, and reports a path as changed when the IDs
differ. `ls-files` reads the working tree. Nothing in that list opens a **blob** from
history. magus never asks git what a file used to contain, only whether its identity
moved.

So the checkout needs the commit graph and two trees. Cloning the full contents of
every file at every revision is paying for the entire history of the repository to
learn which handful of files a branch touched.

The gap is not small, and it is not fixed. In magus's own repository:

| object kind | count  | on disk |
| ----------- | ------ | ------- |
| blobs       | 26,763 | 77.2 MB |
| trees       | 22,124 | 2.6 MB  |
| commits     | 3,807  | 0.8 MB  |

The part CI reads is 4.2% of what it downloads. That share only gets worse with age:
every commit adds a new version of each file it touched and those versions never
collapse, while trees deduplicate heavily and a commit is a few hundred bytes. A
`fetch-depth: 0` pipeline is on the wrong side of that curve permanently.

## The default: a blobless partial clone

`--filter=blob:none` fetches every commit and every tree and leaves historical file
contents on the server. The checkout still materializes the files at the revision
being built, because those are the ones the working tree needs. Anything that does
reach for an old blob still works: git fetches it on demand.

That is exactly the shape magus wants, and it is why blobless - not treeless, not
shallow - is the right default.

Measured on this repository, cloning the same revision three ways:

| clone                       | `.git` size |
| --------------------------- | ----------- |
| `fetch-depth: 0`            | 67 MB       |
| `--filter=blob:none`        | 14 MB       |
| blobless, depth-bounded     | 11 MB       |

### GitHub Actions

[`actions/checkout`](https://github.com/actions/checkout) takes a `filter` input:

```yaml
- uses: actions/checkout@v5
  with:
    fetch-depth: 0
    filter: blob:none
```

Keep `fetch-depth: 0`. It is not the expensive half - with `filter` set it fetches the
commit graph and nothing else, and it keeps `git describe --tags` and any
base-branch diff working no matter how old the branch point is.

### GitLab CI

GitLab clones through its own runner settings rather than a script step:

```yaml
variables:
  GIT_DEPTH: 0
  GIT_STRATEGY: clone
  # Requires GitLab 15.2+ on the server side.
  GIT_FETCH_EXTRA_FLAGS: --filter=blob:none
```

### CircleCI, Buildkite, Jenkins, and anything script-driven

Providers that hand you a shell do not need a special integration - the clone is just
git:

```sh
git clone --filter=blob:none --no-checkout "$REPO_URL" repo
cd repo
git checkout "$COMMIT_SHA"
```

For Jenkins declarative pipelines the same filter is available through the git plugin's
`honorRefspec`/`CloneOption` behaviors; set the shallow option off and add
`--filter=blob:none` to the clone options.

### Azure Pipelines

```yaml
steps:
  - checkout: self
    fetchDepth: 0
    fetchFilter: blob:none # Azure DevOps sprint 222 and later
```

## Bounding the depth too

Blobless makes the payload small; it is still linear in the age of the repository,
because the commit graph keeps growing. Bounding the depth as well makes the cost
track how far the branch diverged instead - a branch cut three commits ago pays for
three commits.

The reason nobody does this is that a naive shallow clone fails in the worst possible
way. A depth-bounded checkout can hold HEAD and hold the base branch and still not
hold the commit where they diverged, and `git merge-base` then fails **exactly** the
way it fails for a branch that does not exist. magus cannot tell those two apart, so
it does the only safe thing and selects every project. Your pipeline still passes. It
just quietly stopped being incremental, on every run, forever. That is
[MGS1010](../../reference/codes/magusfile/MGS1010.md).

magus handles this itself. When the merge base is missing **and** the repository is
shallow, it fetches progressively more history - depth 32, then 128, 512, 2048 -
until the common ancestor appears, then diffs against it normally. You will see it in
the log when it happens:

```text
INFO deepened shallow clone to reach the merge base base=origin/main depth=32
```

Three things bound the blast radius:

- It only ever touches a **shallow** repository. A full clone that cannot find a merge
  base is reported as the bad ref it is rather than papered over with fetches.
- It only ever **adds** history. `git fetch --depth=N` is absolute in both directions and
  will happily shorten a checkout that arrived deeper, so magus grows what is already
  present with `--deepen` and reserves `--depth` for a base ref it is fetching for the
  first time. A checkout never ends up with less history than it started with, which
  matters when a later step in the same job runs `git describe`.
- A failed fetch **ends** the recovery instead of retrying at a larger depth, so a runner
  with no network fails fast rather than stalling behind four timeouts.

That makes a bounded depth safe to set:

```yaml
- uses: actions/checkout@v5
  with:
    fetch-depth: 50 # magus deepens further if a branch outran it
    filter: blob:none
```

Treat the number as a hint, not a contract. Set it near your typical branch length and
let magus cover the tail. If the log shows it deepening on most runs, the hint is too
low and you are paying an extra round trip to learn that.

Do not bound the depth on a job that runs `git describe --tags` (a release build
resolving a version, for example). Its cost is the distance to the nearest tag, which
a depth bound will cut through.

## Two filters that look better and are not

**`--filter=tree:0`** (treeless) is smaller still, and it costs more than it saves here. It omits the
trees `git diff --name-only` compares, so git refetches them one commit at a time. On
this repository a single 40-commit-back name-only diff triggered **10** lazy fetches to
save 3 MB over blobless. Each of those fetches is a network round trip.

**`sparse-checkout`** limits which files land in the working tree. affected reads the
diff, not the working tree, so a sparse checkout does not shrink the fetch that
matters, and it will hide files from targets that legitimately need them.

## Verifying it worked

A partial clone degrades **silently** when the server does not support it: git prints
`warning: filtering not recognized by server, ignoring` and hands you a full clone
anyway. GitHub and GitLab support it; a self-hosted mirror may not, and
`uploadpack.allowFilter` is the server config to check.

Confirm the filter is live in the checkout itself:

```sh
git config remote.origin.partialclonefilter # -> blob:none
git rev-parse --is-shallow-repository       # -> true when depth-bounded
```

Then confirm magus agrees about the diff, which is the thing you actually care about:

```sh
magus doctor
magus affected --explain <project>
```

`magus doctor` probes the base ref directly and reports a `vcs base ref` failure when
it does not resolve. `--explain` names the changed file that put a project in the set,
so an over-selected run is visible instead of merely slow. If a CI run selects every
project, look for MGS1010 before looking anywhere else.

## See also

- [affected.md](../../concepts/workspace/affected.md) - how the diff becomes a project set.
- [MGS1010](../../reference/codes/magusfile/MGS1010.md) - the diagnostic for an uncomputable diff.
- [ci.md](../../concepts/targets/ci.md) - the `ci` anchor these pipelines run.
- [daemon.md](daemon.md) - concurrency when several CI steps share a machine.
