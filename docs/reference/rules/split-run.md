---
title: "split-run: the same target run again on a different project set, on one line or as a separate call"
description: "An advisory: it explains, and blocks nothing, on the same target run again on a different project set, on one line or as a separate call."
tags: [guard, rules, split-run, advise]
---

# split-run

An advisory: it explains, and blocks nothing, on the same target run again on a different project set, on one line or as a separate call.

## What it catches

The same target run again on a different project set, on one line or as a separate call.

## Why

`magus run` and `magus affected` take one target and many projects, so the same target run twice on two project sets is usually one call typed as two: `magus run lint . docs` covers what `magus run lint .` and `magus run lint docs` would otherwise cost as two workspace loads. It fires on TWO shapes. On one line (`magus run lint . && magus run lint docs`), it narrows the chained-run text to the combined form; a chain of genuinely different targets stays chained-run's text and domain. Across two separate calls, it compares the session's last magus run/affected invocation against this one: same target, same charms, a different project set, inside a ten-minute window. Charms count as part of the target identity, so `lint` and `lint:rw` are never combined into one call. Held to one firing per session for the cross-call shape; the one-line shape speaks every time, like chained-run beside it.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [split-run]: ...
```

`magus describe rule split-run` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
