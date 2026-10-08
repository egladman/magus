---
title: "busy-wait: a loop that only sleeps between polls, holding a tool slot for its whole wait"
description: "An advisory by default: it explains, and blocks nothing, on a loop that only sleeps between polls, holding a tool slot for its whole wait."
tags: [guard, rules, busy-wait, advise]
---

# busy-wait

An advisory by default: it explains, and blocks nothing, on a loop that only sleeps between polls, holding a tool slot for its whole wait.

## What it catches

A loop that only sleeps between polls, holding a tool slot for its whole wait.

## Why

A backgrounded command is tracked and announces its own completion, so starting it and doing something else is strictly better than watching it. A loop probing a process the caller did not start (`kill -0`, the process table, `magus status` or a job row) gets no such announcement, and its deny says only what holds for it: the loop occupies a tool slot for the whole wait. The loop also has no bound of its own: past the tool timeout it is BACKGROUNDED rather than killed, and goes on polling a condition that may never arrive, because a run that failed early never prints the line being grepped for. Several have had to be killed by hand. Waiting on something OUTSIDE this machine, a remote queue or a deploy nobody here started, is what a host's monitor is for.A shell script is judged by its content, so `bash wait.sh` and a write of wait.sh get the verdict the loop would get typed inline; so do the output-pipe, output-redirect and unknown-env rules.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"busy-wait": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [busy-wait]: ...
```

`magus describe rule busy-wait` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
