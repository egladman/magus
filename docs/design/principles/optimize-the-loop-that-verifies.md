---
title: Optimize the loop that verifies, never the loop that generates
order: 3
description: Every mechanism makes knowing and trusting a change cheaper; nothing in magus exists to make producing code faster.
tags: [design, principle, verification]
---

# Optimize the loop that verifies, never the loop that generates

Producing code was never the bottleneck. Reviewing it and trusting it were,
and both got harder as generating got cheap. So every mechanism here attacks
the verify side: the cache makes re-checking free, `affected` makes the check
minimal, the drift gate makes generated output self-verifying, the explain
lenses make verdicts inspectable, `describe file` makes a diff triageable.
Nothing in magus exists to make producing code faster. That is a commitment,
not a gap.

The test for a proposed feature is whose loop it accelerates. A pitch of
"produce more, faster" fails by construction; "know sooner, trust cheaper" is
at least aimed at the right loop. This prevents magus from becoming a tool
whose product is throughput: volume is the one metric that gets easier to
move every year and proves less every year, and a build tool that helps ship
more unverified work faster has joined the problem it was built against.

The horizon for that test is years. A tool that generates on your behalf
compounds dependence: what its operator can ship drifts past what they can
judge, and the gap is the crutch. A tool that verifies compounds judgment,
one answered question at a time.

## Enforced by

- Review against the [scope test](../../scope.md#the-test): a feature names
  the verify step it shortens.
- [Automation you can interrogate](../judgment/automation-you-can-interrogate.md)
  holds the enforcement half: helpful, never so helpful that the operator stops
  learning.

## Good

Each of these shortens the time to a trustworthy answer about a change that
already exists:

```sh
magus affected ci
magus describe file internal/config/load.go
magus affected ci --explain
```

## Bad

```sh
nx generate @nx/react:component Button
```

A scaffolder in the build tool makes more code to review, faster. It moves the loop magus
exists to keep small, and the review it adds is the cost it hides.

## See also

- [Automation you can interrogate](../judgment/automation-you-can-interrogate.md)
- [Built for the expensive era](built-for-the-expensive-era.md)
- [Capability is not a reason](capability-is-not-a-reason.md)
