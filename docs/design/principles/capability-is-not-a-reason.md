---
title: Capability is not a reason
order: 2
description: The default answer to a new capability is no; the burden of proof is a failure removed, never a preference enabled.
tags: [design, principle, scope]
---

# Capability is not a reason

Building the wrong thing got cheap. Coherent, well-structured, completely
misconceived work can be generated in minutes, so a design reading well
proves less than it used to, and someone still has to say no to work that
would otherwise ship. The default answer to a new capability is no, and the
burden of proof is a failure removed, never a preference enabled. The version
window needed four dead designs before one passed that bar; the
[knobs rule](../../scope.md#the-knobs) is the same principle applied to options.

It prevents the accretion this project was a reaction to (each addition
defending itself, nobody removing anything), now accelerated, because writing
the unnecessary thing costs nearly nothing while maintaining it costs what it
always did.

Prior art is input, never the justification. "Another tool has it" says the
idea exists; it says nothing about the failure it would remove here.

## Enforced by

- The [scope test](../../scope.md#the-test), applied in review by reading the
  diff for what the tool had to learn.
- [A record of refusals](../refusals.md), where each no is kept with what
  decided it.

## Good

A proposal that names the failure it removes and the evidence for it:

```text
Problem: 22 of 203 sessions ran with no guard, and nothing said so.
Change:  the hook template announces each fail-open arm.
Removes: a guard that stopped judging looking like a session with nothing to deny.
```

## Bad

```text
Add `--watch-all`. Nx has it, and it would be nice for large repos.
```

It names a tool that has the feature and a preference for it, and no failure
anyone hit. Nothing in it can be checked, so nothing in it can be refused on
evidence either.

## See also

- [Scope](../../scope.md)
- [A record of refusals](../refusals.md)
- [Optimize the loop that verifies, never the loop that generates](optimize-the-loop-that-verifies.md)
