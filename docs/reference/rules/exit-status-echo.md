---
title: "exit-status-echo: a line ending by printing an exit status, which the harness already reports"
description: "An advisory by default: it explains, and blocks nothing, on a line ending by printing an exit status, which the harness already reports."
tags: [guard, rules, exit-status-echo, advise]
---

# exit-status-echo

An advisory by default: it explains, and blocks nothing, on a line ending by printing an exit status, which the harness already reports.

## What it catches

A line ending by printing an exit status, which the harness already reports.

## Why

The harness reports a nonzero exit on its own and success needs no confirmation, so `cmd; echo "rc=$?"` adds lines and no information. It also misreports: the echo exits 0, so the line as a whole passes whatever `cmd` did. Every spelling of that ending fires: `echo`/`printf` of `$?` with literal text, to the console or stderr; the same through a capture (`rc=$?; echo $rc`); `cmd || echo "failed $?"`, where the echo runs exactly when cmd failed; and `${PIPESTATUS[...]}`, whose answer is `set -o pipefail` or no pipe. `exit $?`, `[ $? -ne 0 ]`, an echo mid-script, one after `&&`, and one redirected to a file keep the status for later logic and are untouched. Chain with `&&`, or make separate calls, when a failure must not be masked.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"exit-status-echo": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [exit-status-echo]: ...
```

`magus describe rule exit-status-echo` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
