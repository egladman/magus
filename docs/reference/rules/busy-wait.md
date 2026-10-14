---
title: "busy-wait: a while or until loop that sleeps, or a for loop that sleeps until it breaks out"
description: "An advisory by default: it explains, and blocks nothing, on a while or until loop that sleeps, or a for loop that sleeps until it breaks out."
tags: [guard, rules, busy-wait, advise]
---

# busy-wait

An advisory by default: it explains, and blocks nothing, on a while or until loop that sleeps, or a for loop that sleeps until it breaks out.

## What it catches

A while or until loop that sleeps, or a for loop that sleeps until it breaks out.

## Why

A backgrounded command is tracked and announces its own completion, so starting it and doing something else is strictly better than watching it. Whatever the loop tests (a process, a file test on a task's output file, a grep of it) and whatever else each pass runs, a loop that sleeps is waiting: wait for the task's completion notice, or `magus job wait <job>` for a job; `magus status --watch` is for a person watching. A loop probing a process the caller did not start (`kill -0`, the process table, `magus status` or a job row) gets no such announcement, so its deny names the record magus keeps of that work instead: `magus job wait <job>` for a job, `magus status --watch` for the project lock. The loop also has no bound of its own: past the tool timeout it is BACKGROUNDED rather than killed, and goes on polling a condition that may never arrive, because a run that failed early never prints the line being grepped for. Several have had to be killed by hand. A host's watcher running the same loop is judged the same way, since it is the same poll that also wakes the agent on every line and at expiry. A shell script is judged by its content, so `bash wait.sh` and a write of wait.sh get the verdict the loop would get typed inline; so do the output-pipe, output-redirect and unknown-env rules.

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
deny [busy-wait]: ...
advise [busy-wait]: ...
```

`magus describe rule busy-wait` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
