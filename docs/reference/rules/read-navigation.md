---
title: "read-navigation: a whole read of a mapped Go or Markdown file over 120 lines"
description: "A deny rule: it refuses a whole read of a mapped Go or Markdown file over 120 lines, and names what to run instead."
tags: [guard, rules, read-navigation, deny]
---

# read-navigation

A deny rule: it refuses a whole read of a mapped Go or Markdown file over 120 lines, and names what to run instead.

## What it catches

A whole read of a mapped Go or Markdown file over 120 lines.

## Why

The deny carries the file's declarations or headings with their lines, and the command that prints one of them, so the refused read costs nothing. A Go declaration spans its doc comment to its closing brace, each member of a grouped var, const or type is its own entry, and a method is named `Type.Method`. 120 lines is the p90 of a bounded read; measured 2026-09-26 over 66,548 Bash reads, 8,394 dumped a whole Go, Buzz or Markdown file, and 3.6% of whole reads were followed by an edit of that file. Silent on a short file, Buzz (no symbol index), a generated output, a path outside the workspace, a stale index, a heading count the graph disagrees with, and a read feeding a pipe or redirect.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [read-navigation]: ...
```

`magus describe rule read-navigation` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
