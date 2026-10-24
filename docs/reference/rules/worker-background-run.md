---
title: "worker-background-run: a bound worker running a magus target in the background"
description: "A deny rule by default: it refuses a bound worker running a magus target in the background, and names what to run instead."
tags: [guard, rules, worker-background-run, deny]
---

# worker-background-run

A deny rule by default: it refuses a bound worker running a magus target in the background, and names what to run instead.

## What it catches

A bound worker running a magus target in the background.

## Why

A worker that backgrounds its check ends its turn with the run pending and parks there: nothing reports the result to the session that spawned it, and the background tasks pile up. It fires when the wiring reports that the host runs the call in the background, for a `magus run` or `magus affected` by a worker under a live lease that does not integrate the checkout. A run that only reports (--plan, --dry-run, --graph, --help) passes. A wiring that does not report how the host runs a call leaves it silent, and so does a pre-authorized next, since the host's choice is the call's and not the line's. The answer is a foreground call with a timeout that covers the run; check-timeout names one from the target's last real run, and a check longer than the host's foreground maximum runs under `magus job exec --timeout`, which this rule passes in the background: its completion notice is the wait, and background-left does not refuse the stop while it runs.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"worker-background-run": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [worker-background-run]: ...
```

A workspace that sets this rule to `advise` sees `advise [worker-background-run]: ...` instead.

`magus describe rule worker-background-run` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
