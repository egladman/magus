---
title: "exit-status-echo: a line ending by printing an exit status, which the harness already reports"
description: "A deny rule: it refuses a line ending by printing an exit status, which the harness already reports, and names what to run instead."
tags: [guard, rules, exit-status-echo, deny]
---

# exit-status-echo

A deny rule: it refuses a line ending by printing an exit status, which the harness already reports, and names what to run instead.

## What it catches

A line ending by printing an exit status, which the harness already reports.

## Why

The harness reports a nonzero exit on its own and success needs no confirmation, so `cmd; echo "rc=$?"` adds lines and no information. It also misreports: the echo exits 0, so the line as a whole passes whatever `cmd` did. Every spelling of that ending fires: `echo`/`printf` of `$?` with literal text, to the console or stderr; the same through a capture (`rc=$?; echo $rc`); `cmd || echo "failed $?"`, where the echo runs exactly when cmd failed; and `${PIPESTATUS[...]}`, whose answer is `set -o pipefail` or no pipe. Measured 2026-09-24: 508 lines ended this way against 2 denies, when only a bare trailing `echo $?` fired. `exit $?`, `[ $? -ne 0 ]`, an echo mid-script, one after `&&`, and one redirected to a file keep the status for later logic and are untouched. Chain with `&&`, or make separate calls, when a failure must not be masked.

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
