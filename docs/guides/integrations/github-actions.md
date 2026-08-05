---
title: GitHub Actions
description: Wiring magus into GitHub Actions - the three actions magus publishes, a workflow that shards by affected project, remote caching over the Actions cache service, and the annotations and pull request advice that come with it.
tags: [ci, github, actions, cache, remote-cache, sharding, affected, annotations]
---

# GitHub Actions

magus publishes three composite actions. Each does one thing, and a workflow reaches for
as few of them as it needs.

| action | what it does |
| --- | --- |
| `setup-magus` | installs magus and puts it on PATH |
| `magus` | runs a magus command, writes the run summary, or merges shard histories |
| `advice` | leaves [pull request advice](pr-advice.md) on what your build graph noticed |

(magus's own repository carries a fourth, `setup-workspace`, which wraps `setup-magus`
with the mise toolchain and system packages its targets happen to need. That is this
repository's preamble rather than part of magus, and it is not meant to be consumed.)

Reference them from a tag, never a branch:

```yaml
- uses: egladman/magus/.github/actions/setup-magus@v0.4.0
```

## The smallest workflow that works

```yaml
name: CI
on:
  pull_request:
  push:
    branches: [main]

jobs:
  ci:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
        with:
          fetch-depth: 0
          filter: blob:none

      - uses: egladman/magus/.github/actions/setup-magus@v0.4.0

      - run: magus affected ci
```

`fetch-depth: 0` is not optional. `affected` compares against a merge base, and a shallow
clone has none; `filter: blob:none` keeps the full history affordable by fetching file
contents only when something reads them. See [CI checkout](ci.md) for why that pairing is
the right default and what a shallow clone costs you instead.

## Installing magus

`setup-magus` takes three inputs, and the interesting one is `installation-strategy`:

| strategy | what it installs |
| --- | --- |
| `automatic` | a verified release, falling back to a source build |
| `prebuilt` | the release named by `git-ref`, checksum-verified |
| `source` | the magus that `source-path` defines |

Reach for `source` when the workspace under test needs a magus that has not been released
yet - a magusfile using a feature from this commit. Reach for `prebuilt` with an explicit
`git-ref` everywhere else: it is faster, and it pins what ran.

If a source build is in play, note the PATH order: it provisions its own Go and prepends
it, so a job that pinned a toolchain has to put its own back in front afterwards.

The checkout stays in your job. A local composite action cannot contain the checkout that
makes the action loadable in the first place.

## Sharding by affected project

One job that runs everything wastes a matrix. magus computes the affected set once, splits
it into shards, and each shard runs only its own projects.

```yaml
jobs:
  plan:
    runs-on: ubuntu-latest
    outputs:
      matrix: ${{ steps.plan.outputs.matrix }}
      count: ${{ steps.plan.outputs.count }}
    steps:
      - uses: actions/checkout@v5
        with: { fetch-depth: 0, filter: blob:none }
      - uses: egladman/magus/.github/actions/setup-magus@v0.4.0
      - id: plan
        run: magus affected ci --plan | magus run ci-shard:gha

  ci:
    needs: plan
    if: fromJSON(needs.plan.outputs.count) > 0
    strategy:
      matrix: ${{ fromJSON(needs.plan.outputs.matrix) }}
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
        with: { fetch-depth: 0, filter: blob:none }
      - uses: egladman/magus/.github/actions/setup-magus@v0.4.0
      - uses: egladman/magus/.github/actions/magus@v0.4.0
        with:
          command: affected ci --shard ${{ matrix.shard }}
          shard: ${{ matrix.shard }}
          n-shards: ${{ matrix.total }}
```

`magus affected ci --plan` emits the plan; `magus run ci-shard:gha` translates it into
matrix outputs. The `gha` charm is what writes `$GITHUB_OUTPUT` - without it the plan is
printed and nothing else, which is what you want when running the same command locally.

`count` guards the matrix: when nothing is affected there is no job to run, and a matrix
of zero shards is an error rather than a skip.

Passing `shard` and `n-shards` to the action sets `MAGUS_SHARD` and `MAGUS_N_SHARDS`, so
shard-aware output lands in the run's timing events without extra shell in the workflow.

## Remote caching

magus can use the Actions cache service as a shared cache, so a target another shard
already built replays instead of running again. Wire the bundled spell into your
magusfile:

```buzz
import "spells/github/actions" as github;

magus\cache.remote(github);
```

The spell reads `ACTIONS_RESULTS_URL` and `ACTIONS_RUNTIME_TOKEN`, which the runner does
not export to a plain `run:` step. Add the step that exports them to any job that should
share the cache:

```yaml
- uses: crazy-max/ghaction-github-runtime@v3
```

Everywhere else the spell reports itself disabled: it probes `GITHUB_ACTIONS` first and
skips fetch and push entirely off a runner, so a magusfile carrying this line stays a
no-op on a laptop. No remote calls, nothing to configure, nothing to turn off.

See [Remote cache](../../concepts/cache/remote.md) for what gets stored, how entries are
keyed, and the guarantees a shared cache does and does not give you.

## Annotations and folded logs

The same spell teaches magus how GitHub renders a job log:

```buzz
magus\ci.provider(github);
```

Failures become `::error::` annotations that surface inline on the pull request, and each
target's output folds into its own group. A declared provider wins over magus's built-ins,
so a workspace can swap in its own spell for another CI system.

## Reporting at the end of a run

One job, after the shards, for everything that describes the run:

```yaml
  report:
    needs: [plan, ci]
    if: always() && needs.plan.result == 'success'
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v5
        with: { fetch-depth: 0, filter: blob:none }
      - uses: egladman/magus/.github/actions/setup-magus@v0.4.0
      - uses: egladman/magus/.github/actions/magus@v0.4.0
        with:
          report: 'true'
          ci-result: ${{ needs.ci.result }}
          shard-count: ${{ needs.plan.outputs.count }}
          merge-history: ${{ github.ref == 'refs/heads/main' }}
```

`report` writes the outcome and the workspace's insight report to the step summary.
`merge-history` folds each shard's run history into the persisted one, which is what makes
volatility and timing data accumulate across runs - on main only, since a pull request's
history describes a branch about to disappear.

## Pull request advice

```yaml
- uses: egladman/magus/.github/actions/advice@v0.4.0
```

One comment describing what your build graph noticed: generated files edited by hand,
files no project claims, a change that reaches most of the workspace. Every advisor is an
input, and every one can be silenced per pull request with a label. See
[Pull request advice](pr-advice.md).

## Permissions

| job | needs |
| --- | --- |
| running targets | `contents: read` |
| advice | `pull-requests: write` |
| advice with `fix-generated-drift` | `contents: write` |

On a pull request from a fork the default token is read-only whatever you declare, so the
advice comment and the drift autofix both fail there. That is the platform's rule, not
magus's, and the advisors say so rather than failing silently.

## See also

- [CI checkout](ci.md): clone depth, and why magus deepens a shallow clone rather than
  guessing.
- [CI providers](../../concepts/ci-providers.md): what a provider spell supplies.
- [Remote cache](../../concepts/cache/remote.md): the cache model behind the wiring above.
- [Pull request advice](pr-advice.md): every advisor, and how to turn each one off.
