---
title: Running under CI
order: 10
description: What changes when magus runs on a build server rather than your machine - the pipeline verb, the checkout, the shared cache, and the provider spell that teaches magus one CI system's log format.
tags:
  [
    ci,
    ci-provider,
    affected,
    remote-cache,
    sharding,
    annotations,
  ]
---

# Running under CI

Nothing about a target changes on a build server. The same `ci` target runs the
same ops against the same declared inputs, and the same cache key decides whether
any of it has to run at all. That is the point: a pipeline you cannot reproduce
locally is a pipeline you debug through the web UI.

Five things do change, and each has a page.

## The pipeline verb

`ci` is an ordinary target you compose in your magusfile, not a mode magus enters.
It strips the write-granting charms before it dispatches, so a pipeline cannot
mutate the tree even when someone asks it to. See [CI](targets/ci.md).

## The gate's size

`magus affected ci` runs only the part of the gate a change can reach: nothing
for prose no target reads, the drift check and lint for a comment edit, tests
narrowed to the packages that import a changed Go package. Every tier below the
full gate rests on a proof, and the run prints the evidence for every file. See
[Gate sizing](ci/risk.md).

## The checkout

A build server starts from nothing, and how you clone decides what magus can
compute. `magus affected` needs a base to diff against, so a shallow clone that
omits it silently degrades to running everything. See
[CI checkout](../guides/integrations/ci.md).

## The shared cache

Local caching helps one machine. A [remote cache provider](cache/remote.md) lets
runners replay artifacts another machine already built, under a trust model that
refuses unsigned entries.

## The provider spell

magus knows no vendor's log syntax. A [CI provider](ci/providers.md) teaches it
one: fold markers around a failure, and annotations that surface on a pull
request. Wire it with `magus\ci.provider(<spell>)`.

For a worked example on one system, see
[GitHub Actions](../guides/integrations/github-actions.md).
