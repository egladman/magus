---
title: "read-navigation: a whole read of a Go, Buzz or Markdown file past the workspace's line limit"
description: "A deny rule by default: it refuses a whole read of a Go, Buzz or Markdown file past the workspace's line limit, and names what to run instead."
tags: [guard, rules, read-navigation, deny]
---

# read-navigation

A deny rule by default: it refuses a whole read of a Go, Buzz or Markdown file past the workspace's line limit, and names what to run instead.

## What it catches

A whole read of a Go, Buzz or Markdown file past the workspace's line limit.

## Why

The deny carries the file's declarations or headings with their lines, and the command that prints one of them, so the refused read costs nothing. The map is parsed from the file itself, so a stale index still gets one: where the index vouches for every name, `magus refs <name> --definition --source` prints one checked against it and the graph lists the map; where it does not, and for Buzz, which nothing indexes for refs, `sed -n <first>,<last>p <file>` prints one by its lines. A Go declaration spans its doc comment to its closing brace, each member of a grouped var, const or type is its own entry, and a method is named `Type.Method`. A Buzz entry is a top-level fun, test, object or enum, from its doc comment to the line before the next statement. Every file a `cat` or `nl` prints is judged, so `cat a.go b.go` is refused for whichever mapped file is over the threshold. A host's read tool is restated as the same line (`cat <file>`, or `sed -n` over its offset and limit, a limit over 300 counting as whole) and judged the same way. The size worth flagging is the rule's `lines` setting, which a workspace sets: the binary ships no number for it. Silent on a short file, TypeScript (no parser here), SKILL.md, AGENTS.md and a host's own instruction file (written to be read whole), a generated output, a path outside the workspace, a file that does not parse, and a read feeding a pipe or redirect. It refuses each caller once per file: the same caller asking for the same file again has the map in hand and chose the whole read, so that read passes.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"read-navigation": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [read-navigation]: ...
```

A workspace that sets this rule to `advise` sees `advise [read-navigation]: ...` instead.

`magus describe rule read-navigation` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
