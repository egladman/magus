---
title: "check-timeout: a bound worker's magus run whose timeout is shorter than the target's last real run allows for"
description: "A deny rule by default: it refuses a bound worker's magus run whose timeout is shorter than the target's last real run allows for, and names what to run instead."
tags: [guard, rules, check-timeout, deny]
---

# check-timeout

A deny rule by default: it refuses a bound worker's magus run whose timeout is shorter than the target's last real run allows for, and names what to run instead.

## What it catches

A bound worker's magus run whose timeout is shorter than the target's last real run allows for.

## Why

A call the host times out moves to the background, and a worker that then ends its turn parks until the run ends, with nobody told the result. The duration is the newest run of that target and project that really ran, from the newest invocations in the repository's session store; a replay or a run refused before it started does not count. It names a timeout of half again that duration, rounded up to a minute and capped at the host's foreground maximum. When the run needs longer than the maximum it names the same run under `magus job exec --timeout`, to run in the background and wait on by its completion notice. It reads the host's foreground default and maximum from the wiring, which declares them, and the call's own timeout, and is silent when either limit is undeclared, when the target has no recorded run, and for anyone but a worker under a live lease that does not integrate the checkout. Held to one firing per target per session, refusing included: after it a rerun of the same target, narrowed or not, goes through.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"check-timeout": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [check-timeout]: ...
```

A workspace that sets this rule to `advise` sees `advise [check-timeout]: ...` instead.

`magus describe rule check-timeout` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
