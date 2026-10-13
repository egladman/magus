---
title: "sleep-poll: a sleep before a one-shot status read, or a backgrounded call that only sleeps"
description: "An advisory by default: it explains, and blocks nothing, on a sleep before a one-shot status read, or a backgrounded call that only sleeps."
tags: [guard, rules, sleep-poll, advise]
---

# sleep-poll

An advisory by default: it explains, and blocks nothing, on a sleep before a one-shot status read, or a backgrounded call that only sleeps.

## What it catches

A sleep before a one-shot status read, or a backgrounded call that only sleeps.

## Why

Both are a wait whose loop is the caller's own next call. A sleep of ten seconds or more followed on the same line by a status read (`kill -0`, the process table, `magus status` or a job row, a pull request's checks, a CI run) looks once and leaves the next look to the next call. A backgrounded call that only sleeps and prints is a timer: its notice says only that time passed, where the work's own backgrounded call is announced with its result. A watch (`--watch`) is not a status read, and a loop is busy-wait's. It cannot tell a one-off settle before a single check from the tenth look of a poll; the served watch answers both.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"sleep-poll": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [sleep-poll]: ...
```

`magus describe rule sleep-poll` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
