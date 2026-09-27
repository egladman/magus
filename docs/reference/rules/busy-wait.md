---
title: "busy-wait: a loop that only sleeps between polls, holding a tool slot for its whole wait"
description: "A deny rule: it refuses a loop that only sleeps between polls, holding a tool slot for its whole wait, and names what to run instead."
tags: [guard, rules, busy-wait, deny]
---

# busy-wait

A deny rule: it refuses a loop that only sleeps between polls, holding a tool slot for its whole wait, and names what to run instead.

## What it catches

A loop that only sleeps between polls, holding a tool slot for its whole wait.

## Why

A backgrounded command is tracked and announces its own completion, so starting it and doing something else is strictly better than watching it. A loop probing a process the caller did not start (`kill -0`, the process table, `magus status` or a job row) gets no such announcement, and its deny says only what holds for it: the loop occupies a tool slot for the whole wait. The loop also has no bound of its own: past the tool timeout it is BACKGROUNDED rather than killed, and goes on polling a condition that may never arrive, because a run that failed early never prints the line being grepped for. Several have had to be killed by hand. Waiting on something OUTSIDE this machine, a remote queue or a deploy nobody here started, is what a host's monitor surface is for. A shell script is judged by its content, so `bash wait.sh` and a write of wait.sh get the verdict the loop would get typed inline; so do the output-pipe, output-redirect and unknown-env rules.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [busy-wait]: ...
```

`magus describe rule busy-wait` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
