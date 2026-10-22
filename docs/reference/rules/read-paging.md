---
title: "read-paging: a mapped file read window by window, past two windows of it"
description: "A deny rule by default: it refuses a mapped file read window by window, past two windows of it, and names what to run instead."
tags: [guard, rules, read-paging, deny]
---

# read-paging

A deny rule by default: it refuses a mapped file read window by window, past two windows of it, and names what to run instead.

## What it catches

A mapped file read window by window, past two windows of it.

## Why

read-navigation refuses a whole read and serves the file's map; paging through the file instead spends the same lines over several calls. Each caller's windows of each Go, Buzz or Markdown file are counted, and the third distinct one is refused with the map. A window inside one declaration or heading is a targeted read and never counts, nor does a repeat of a window already read, a whole read, or a file short enough for read-navigation to pass whole. The refusal names one concrete declaration or heading to read, the one holding the refused window's first line or the next one down, as the `sed -n` span or the `refs` command that prints it. Reading that span is a declaration read, so the suggested window never counts as a page; a window one line either side of a declaration (the Read tool's offset and limit land one off) counts as inside it. It cannot tell a walk through the file from reads of three spans that each cross a declaration's edge.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"read-paging": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [read-paging]: ...
```

A workspace that sets this rule to `advise` sees `advise [read-paging]: ...` instead.

`magus describe rule read-paging` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
