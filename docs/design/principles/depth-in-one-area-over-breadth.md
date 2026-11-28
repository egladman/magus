---
title: Depth in one area over breadth
order: 45
description: A thing magus or its libraries ship is complete and correct, or it is absent; a finished version of one thing beats a half-built version of several.
tags: [design, principle, scope]
---

# Depth in one area over breadth

Scope creep is the enemy, and the kind I watch for most is not one feature too
many. It is several features, each built a little. A module that does
everything in excess pays for it in every caller's experience: each half-built
piece needs its own workaround, its own caveat in the docs and its own doubt
about whether it can be trusted. A finished version of one thing is worth more
than a half-built version of several, so a thing magus or its libraries ship is
complete and correct, or it is absent.

Depth means the depth of each piece shipped. A primitive does its one job all
the way: it answers every case it accepts correctly, refuses by name every case
it cannot answer, and has tests for the use it was built for and for one it was
not. Depth does not mean picking one partner. magus works with several agent
hosts, outside engines and forges, and each integration is held to this bar.
The rule is that each one is finished, not that there is only one.

One area is counted per thing shipped, not per binary.
[Unix survives at the verb, not the binary](unix-survives-at-the-verb.md) says
a monorepo tool has to understand many things, and this page agrees: the
binary is as big as the problem, and each verb, package and primitive inside it
goes deep on its own question.

The test for a proposal: which core area does this deepen, and what does it
make us refuse? A proposal that cannot name the area, or that opens a new area
and fills it halfway, waits until it can.

The pages beside this one rule the neighbouring ideas:

- [What we do not build is the product](what-we-do-not-build.md) says refusal
  defines the tool. This page says what the tool does build is built all the
  way.
- [Capability is not a reason](capability-is-not-a-reason.md) asks whether a
  capability should exist. This page asks, of one that should, whether it is
  finished.
- [Abstract at the right layer, or not at all](abstract-at-the-right-layer.md)
  refuses a layer too thin to hide anything. This page refuses a feature too
  shallow to rely on, however its layers are drawn.
- [Break now, hold still later](break-now-hold-still-later.md) refuses pushing
  a half-built abstraction onto users, on compatibility grounds. This page
  refuses shipping it at all.
- [Delete before you add](delete-before-you-add.md) removes a half-built
  feature already in the tree. This page keeps one from arriving.

## Enforced by

Nothing mechanical judges whether a thing is complete; that takes a reader.

- Review against this page, with the question above, and the
  [scope test](../../scope.md#the-test) for what the tool had to learn.
- An ADR for a new package or module, saying what it covers and what it leaves
  out. For `libs/safetyscissors`, the 2026-11-28 amendment to
  [ADR 0050](../../decisions/0050-the-harness-is-a-module-named-safetyscissors.md),
  proposed in pull request #1016, states the module's own test for a building
  block.
- [A record of refusals](../refusals.md) keeps what was closed for failing it.
- One narrow piece is mechanical: the `stubs-never-reach-main` analyzer refuses
  a function whose whole body is still its spell's stub, so a placeholder never
  merges as the thing it stands for.

## Good

The attribute-rule package `access` in `libs/safetyscissors`, proposed in pull
request #1016, judges whether a party may do an act to a target, and nothing
else. It has no role type and no file, path or code vocabulary; the principal,
the target and the act's detail are the caller's own types. A declared act with
no permit that holds is denied, and the reason lists the permits that would have
allowed it. A forbid beats a permit, and nothing answers yes. It is under 400
lines, and its tests are a support desk judging refunds and a research pipeline
judging drafts, two uses with nothing to do with code:

```go
accounts, err := access.New(access.Scope[clerk, invoice, struct{}]{
    Name: "accounts",
    Acts: []access.Act{"pay"},
    Rules: []access.Rule[clerk, invoice, struct{}]{
        access.Permit("pay-own-team", "pay", "the clerk is on the invoice's team", ownTeam),
        access.Forbid("large-invoice", "pay", verdict.Ask, "an invoice over 1000 needs a person", large),
    },
})
```

## Bad

A package for bounding work that continues across turns, `wake` in
`libs/safetyscissors`, pull request #1019:

```text
wake: Terms, Store, Decide, MemoryLog. 1,690 lines, 141 exported names, no caller.
Nothing in the package sleeps, schedules or starts a turn.
```

It was designed over three rounds and built, with its own store and record, and
then closed. Research into the repository's own incident record found no
incident it would have prevented. It had the bounds without the clock: it said
whether another turn may start and left the waiting and the scheduling, the hard
half, to a caller that did not exist. It would also have been a second home for
an idea the job row already carries, which marks a row stale after an idle limit
and grades whether its work is done.

## See also

- [What we do not build is the product](what-we-do-not-build.md)
- [Capability is not a reason](capability-is-not-a-reason.md)
- [Delete before you add](delete-before-you-add.md)
- [Unix survives at the verb, not the binary](unix-survives-at-the-verb.md)
