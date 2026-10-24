---
title: "filter-without-input: a filter or interpreter with no file, script, pipe or redirect, which reads a stdin nothing feeds"
description: "A deny rule by default: it refuses a filter or interpreter with no file, script, pipe or redirect, which reads a stdin nothing feeds, and names what to run instead."
tags: [guard, rules, filter-without-input, deny]
---

# filter-without-input

A deny rule by default: it refuses a filter or interpreter with no file, script, pipe or redirect, which reads a stdin nothing feeds, and names what to run instead.

## What it catches

A filter or interpreter with no file, script, pipe or redirect, which reads a stdin nothing feeds.

## Why

A filter given no input reads stdin, and under an agent harness stdin is the harness's own: where the harness holds it open, nothing writes to it and nothing closes it, so the call waits past the tool timeout and goes on waiting in the background. Name the input: a file operand, a pipe into the command, or a `<`, `<<` or `<<<` redirect on it or on a loop or block around it. It reads each tool's own flag grammar, so `grep -e pat file`, `jq --arg k v . f` and `head -n 5 file` are fed, and `tr`, `tee` and `xargs` fire whenever nothing feeds them, because their operands are never input. python, node, perl, ruby and the shells fire given `-` or no script and no inline program, and a shell given `-s`: a `python3 -` whose heredoc sat on a later `cat` waited eight hours. `read` fires without `-t` or `-u`. A recursive grep with no path passes: GNU grep, macOS's BSD grep and the ugrep a host may put behind `grep` all search the working directory then. What the guard cannot classify passes, because it refuses only what it can prove: an unknown flag, an unquoted expansion that may split into several words, `jq -n`, an awk program with a BEGIN block, a command inside a function body. ripgrep with no path passes for the same reason: it searches the working directory unless stdin is a pipe or a file, which a hook cannot see.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"filter-without-input": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [filter-without-input]: ...
```

A workspace that sets this rule to `advise` sees `advise [filter-without-input]: ...` instead.

`magus describe rule filter-without-input` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
