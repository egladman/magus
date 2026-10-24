---
title: "background-unbounded: a shell command run in the background with nothing to end it"
description: "A deny rule by default: it refuses a shell command run in the background with nothing to end it, and names what to run instead."
tags: [guard, rules, background-unbounded, deny]
---

# background-unbounded

A deny rule by default: it refuses a shell command run in the background with nothing to end it, and names what to run instead.

## What it catches

A shell command run in the background with nothing to end it.

## Why

A backgrounded command outlives the agent that started it, and the host stops it only when asked. It fires when the wiring reports that the host runs the call in the background and some command in the line is not bounded: `timeout N` or `gtimeout N` in front of it (stock macOS ships neither; Homebrew's coreutils installs `gtimeout`), a magus target run (bounded by its target's timeout), or a builtin that returns at once. A pipeline is bounded by its first command; a while or until loop never is. The refusal names a ceiling, `timeout 30m` unless ceiling_minutes sets another, the span after which background-silent starts speaking of a quiet task. timeout signals its own process group, so a child that starts its own session escapes it; `--kill-after` ends a command that ignores TERM. A watcher's command is bounded by its own expiry and is not judged here.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"background-unbounded": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [background-unbounded]: ...
```

A workspace that sets this rule to `advise` sees `advise [background-unbounded]: ...` instead.

`magus describe rule background-unbounded` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
