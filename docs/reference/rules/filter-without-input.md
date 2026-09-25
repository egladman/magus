---
title: "filter-without-input: a filter with no file, pipe or redirect, which reads a stdin nothing feeds"
description: "A deny rule: it refuses a filter with no file, pipe or redirect, which reads a stdin nothing feeds, and names what to run instead."
tags: [guard, rules, filter-without-input, deny]
---

# filter-without-input

A deny rule: it refuses a filter with no file, pipe or redirect, which reads a stdin nothing feeds, and names what to run instead.

## What it catches

A filter with no file, pipe or redirect, which reads a stdin nothing feeds.

## Why

A filter given no input reads stdin, and under an agent harness stdin is the harness's own: where the harness holds it open, nothing writes to it and nothing closes it, so the call waits past the tool timeout and goes on waiting in the background. Measured: one such `grep` held a subagent for two hours. Name the input: a file operand, a pipe into the command, or a `<`, `<<` or `<<<` redirect on it or on a loop or block around it. It reads each tool's own flag grammar, so `grep -e pat file`, `jq --arg k v . f` and `head -n 5 file` are fed, and `tr`, `tee` and `xargs` fire whenever nothing feeds them, because their operands are never input. A recursive grep with no path fires too: GNU grep searches `.` then, but macOS's BSD grep reads stdin, and writing `.` costs nothing. What the guard cannot classify passes, because it refuses only what it can prove: an unknown flag, an unquoted expansion that may split into several words, `jq -n`, an awk program with a BEGIN block, a command inside a function body. ripgrep with no path passes for the same reason: it searches the working directory unless stdin is a pipe or a file, which a hook cannot see.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [filter-without-input]: ...
```

`magus describe rule filter-without-input` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
