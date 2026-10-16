---
title: "sleep-poll: a sleep before a status read, a call that only sleeps, or a worker waiting for the build budget"
description: "A deny rule by default: it refuses a sleep before a status read, a call that only sleeps, or a worker waiting for the build budget, and names what to run instead."
tags: [guard, rules, sleep-poll, deny]
---

# sleep-poll

A deny rule by default: it refuses a sleep before a status read, a call that only sleeps, or a worker waiting for the build budget, and names what to run instead.

## What it catches

A sleep before a status read, a call that only sleeps, or a worker waiting for the build budget.

## Why

Both are a wait whose loop is the caller's own next call. A sleep of ten seconds or more followed on the same line by a status read (`kill -0`, the process table, `magus status` or a job row, a pull request's checks, a CI run) looks once and leaves the next look to the next call. A call whose only work is sleeping, printing and reading a file once (`sleep 300; echo tick`, `sleep 120 && tail -5 out.log`, a `for` loop of sleeps and echoes) is a timer: its notice says only that time passed, where the work's own backgrounded call is announced with its result. Backgrounded it is refused at any length; in the foreground from ten seconds, since a foreground sleep past the tool timeout is moved to the background and becomes one. A shorter foreground sleep is a settle, and a line that also builds, tests or runs a script is work. A watch (`--watch`) is not a status read, and a loop is busy-wait's. A worker (a session whose lease is a child row or a subagent's) never waits for the build budget, in a shell call or a watcher: `magus status --wait` or `--watch`, a status read beside a sleep or inside a watcher, or a magus run after a sleep of ten seconds or more. It commits, reports to its orchestrator that its check found no seat, and stops; the orchestrator waits once for every worker, and a spawn or `magus job fork` into a full budget tells it how full the budget is and which run has held it longest. It cannot tell a one-off settle before a single check from the tenth look of a poll; the served watch answers both.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"sleep-poll": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [sleep-poll]: ...
```

A workspace that sets this rule to `advise` sees `advise [sleep-poll]: ...` instead.

`magus describe rule sleep-poll` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
