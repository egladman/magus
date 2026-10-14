---
title: "root-heavy-command: a main thread running delegable commands one after another without spawning"
description: "An advisory by default: it explains, and blocks nothing, on a main thread running delegable commands one after another without spawning."
tags: [guard, rules, root-heavy-command, off]
---

# root-heavy-command

An advisory by default: it explains, and blocks nothing, on a main thread running delegable commands one after another without spawning.

## What it catches

A main thread running delegable commands one after another without spawning.

## Why

A recursive search, a whole-file read of source, a full diff, a target run or a web fetch is work a subagent does from a small context, returning only what matters. Each delegable call since the main thread last spawned or continued a subagent counts; at standard it advises once at the eighth, and at strict it advises at the fifth and refuses from the eighth. Spawning, the final gate, loading a skill, reading Markdown and editing never count, and neither does a command whose output is a ref or one line: the bootstrap build command, `magus run` or `magus affected` with `-s` or `--silent`, `magus run go-build` or `go::go-build`, `magus job fork`, `exit`, `apply` and `wait`, `magus query output --identity`, any magus command asked for `-o name` or `-o template=`, `git commit`, `cherry-pick` and `branch`, `git status -s`, and `git log --oneline` with a count (`-n`, `--max-count` or `-N`). Reading a run's whole output with `magus query output` does count. A calls parameter replaces the level's count.

## Default and override

By default this rule takes the decision `off`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"root-heavy-command": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [root-heavy-command]: ...
advise [root-heavy-command]: ...
```

`magus describe rule root-heavy-command` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
