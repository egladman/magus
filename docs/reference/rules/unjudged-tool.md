---
title: "unjudged-tool: a tool call whose input carries nothing any rule reads, from a tool the guard does not know"
description: "An advisory by default: it explains, and blocks nothing, on a tool call whose input carries nothing any rule reads, from a tool the guard does not know."
tags: [guard, rules, unjudged-tool, advise]
---

# unjudged-tool

An advisory by default: it explains, and blocks nothing, on a tool call whose input carries nothing any rule reads, from a tool the guard does not know.

## What it catches

A tool call whose input carries nothing any rule reads, from a tool the guard does not know.

## Why

Every harness wires every tool to the guard, which judges a call by the shape of its input: a command line, a file path read or written, a search, a spawn's prompt, a skill load. A tool whose input has none of those shapes, and which the guard's known-tool table does not list, could do whatever a rule exists to stop with no rule seeing it; a tool the table lists as touching nothing (a todo list, a plan, a web lookup) passes. It advises by default, since the guard cannot know such a tool is harmful; a workspace that wants every call judged sets it to deny. A finished call is never named.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"unjudged-tool": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [unjudged-tool]: ...
```

A workspace that sets this rule to `deny` sees `deny [unjudged-tool]: ...` instead.

`magus describe rule unjudged-tool` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
