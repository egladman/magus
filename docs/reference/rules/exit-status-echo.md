---
title: "exit-status-echo: a trailing `echo $?`, which repeats an exit status the harness already reports"
description: "A deny rule: it refuses a trailing `echo $?`, which repeats an exit status the harness already reports, and names what to run instead."
tags: [guard, rules, exit-status-echo, deny]
---

# exit-status-echo

A deny rule: it refuses a trailing `echo $?`, which repeats an exit status the harness already reports, and names what to run instead.

## What it catches

A trailing `echo $?`, which repeats an exit status the harness already reports.

## Why

The harness reports a nonzero exit on its own and success needs no confirmation, so `cmd; echo "rc=$?"` adds lines and no information. It also misreports: the echo exits 0, so the line as a whole passes whatever `cmd` did. It fires only on the LAST statement, joined by `;` or a newline, printing nothing but `$?` and literal text. `rc=$?`, `exit $?`, `[ $? -ne 0 ]`, an echo mid-script, one after `&&` or `||`, and one redirected to a file all keep the status for later logic and are untouched. Chain with `&&`, or make separate calls, when a failure must not be masked.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [exit-status-echo]: ...
```

`magus describe rule exit-status-echo` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
