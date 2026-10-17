---
title: "read-paging: a mapped file read window by window, past two windows of it"
description: "An advisory by default: it explains, and blocks nothing, on a mapped file read window by window, past two windows of it."
tags: [guard, rules, read-paging, advise]
---

# read-paging

An advisory by default: it explains, and blocks nothing, on a mapped file read window by window, past two windows of it.

## What it catches

A mapped file read window by window, past two windows of it.

## Why

read-navigation refuses a whole read and serves the file's map; paging through the file instead spends the same lines over several calls. Each caller's windows of each Go, Buzz or Markdown file are counted, and the third distinct one is refused with the map. A window inside one declaration or heading is a targeted read and never counts, nor does a repeat of a window already read, a whole read, or a file short enough for read-navigation to pass whole. It cannot tell a walk through the file from reads of three spans that each cross a declaration's edge.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"read-paging": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [read-paging]: ...
```

A workspace that sets this rule to `deny` sees `deny [read-paging]: ...` instead.

`magus describe rule read-paging` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
