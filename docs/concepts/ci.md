---
title: Running under CI
order: 10
description: "What changes when magus runs on a build server rather than your machine: the pipeline verb, the checkout, the shared cache, and the provider spell that teaches magus one CI system's log format."
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

Six things do change, and each has a page or a section here.

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

## The shards

`magus affected ci:gha --plan` splits the gate across runners. Its unit is one
target an affected project's `ci` asks for through `ctx.needs`, such as `.:test`
or `libs/x:format`, so one large project can fill several runners. magus finds
the units by evaluating each `ci` body the way a dry run does, under the charms
and the args after `--` the shards will run it with. A step behind
`if (!ctx.hasCharm("gha"))` is no unit of a `ci:gha` plan. A target the body
calls as a function is no unit either: the plan names it under `unplanned`, and
no shard runs it.

Each unit weighs what the run history recorded for it. A unit with no history
weighs the workspace fallback, and the plan lists it under `unmeasured`. When no
unit has any history, the plan opens at most 2 shards and says so under
`fallbacks`.

A shard costs one runner's setup plus every distinct target its units need,
prerequisites counted once. Units go longest first to the shard they leave
cheapest. A new shard opens only when it shortens the longest shard by more than
setup plus the prerequisites it would repeat. A shard's predicted peak memory
must fit the machine `ci.machine` names (`memory_mb`, `cores`), never the
host that plans, and the plan records it. With none set the plan says it checked
no memory budget. The same history, change, machine and config give the same plan
on any host.

Each shard lists its `units`, a `label` that digests them, and its `duties`: the
workspace-wide steps placed on exactly one shard. `cache-export` goes to the
shard holding the most root units, `notes-verify` to the shard with the fewest
units. `magus run ci:gha --stdin --shard <id>` reads the saved plan and runs that
shard's units as one invocation. It refuses a plan evaluated under other charms
or args, and a shard whose projects or label disagree with its units.

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
