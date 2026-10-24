---
title: Worth cloning
order: 14
description: Assume magus can be reproduced; build what is better when copied, and publish the judgment a clone cannot recover.
tags: [design, principle, license]
---

# Worth cloning

Assume magus can be reproduced. A model given the binary can recover the
architecture; given the docs, it can recover the rest, and the cost of doing so
falls every year. So no part of this project's value is allowed to rest on that
being hard. What cannot be recovered from the artifact is the judgment that
produced it: which capabilities were refused and why, what was measured before a
mechanism shipped, where the line between the tool and the person was drawn and
what it cost to hold. That record is the product, and it is published on
purpose. A clone that carries it improves the lives of the people who run it,
and a clone that drops it is a different tool wearing the name.

The test for what to build is therefore whether it is better when copied. A
vocabulary that another build tool adopts makes every agent better at both. A
guard rule that another harness lifts protects someone this project never
meets. A measurement that kills a feature here saves the same feature elsewhere.
Each of those returns more to this project when it spreads than it would have
kept by staying scarce, because the thing magus optimizes for, people who can
still fix their own software, is not a market to corner.

This prevents the failure that makes the assumption above dangerous: taking on
debt to build a moat that will not exist, and then measuring the project by what
it managed to keep from people instead of by what it left them able to do.

## Enforced by

- GPL-3.0 makes the copy legal and keeps the copy's improvements public.
- [Scope](../../scope.md#the-line) records that no account, paid tier, or
  capability behind either exists, so there is nothing a clone would have to
  route around.
- [A record of refusals](../refusals.md) and the measurements behind each rule
  in this section are in the tree, not in anyone's head.

## Good

A refusal kept with the measurement that decided it, in
[A record of refusals](../refusals.md):

```text
| an advisory when an agent edits a file it never looked up | not built: 0.8% of 3,560 first edits were never anchored ... |
```

## Bad

```text
Advanced guard rules are available on the Team plan.
```

It builds the moat this page assumes cannot exist, and it keeps the protection
from exactly the people who would have copied it.

## See also

- [A record of refusals](../refusals.md)
- [The standard](../standard.md)
- [Scope](../../scope.md#the-line)
