---
title: "root-context-budget: a session's main thread whose context has grown past its level's size"
description: "Off by default: once a workspace turns it on, it reports a session's main thread whose context has grown past its level's size."
tags: [guard, rules, root-context-budget, off]
---

# root-context-budget

Off by default: once a workspace turns it on, it reports a session's main thread whose context has grown past its level's size.

## What it catches

A session's main thread whose context has grown past its level's size.

## Why

Every call of a session re-sends its whole context, mostly as cache reads, so its cost grows with the square of its length, and a main thread that does its own reading pays for each result on every call after it. The size is the last usage the main transcript recorded, read after each call. At light it advises once at half the model's window, and says nothing for a model the table does not know; at standard it advises once at a fixed size, lower when the account bills per token than on a subscription quota; at strict it advises lower still and refuses the delegable commands (a recursive search, a whole-file read of source, a full diff, a target run, a web fetch) past a second size. A tokens parameter replaces the level's sizes with one, at the rule's decision. It judges only the unleased main thread, and a thread magus inferred rather than one its host named is only advised. A call whose output is a ref or one line is never judged by this rule, so a main thread past its budget can still build ./magus and file its work: the bootstrap build command, `magus run` or `magus affected` with `-s` or `--silent`, `magus run go-build` or `go::go-build`, `magus job fork`, `exit`, `apply` and `wait`, `magus query output --identity`, any magus command asked for `-o name` or `-o template=`, `git commit`, `cherry-pick` and `branch`, `git status -s`, and `git log --oneline` with a count (`-n`, `--max-count` or `-N`). A line that changes directory first is judged by the rest of it.

## Default and override

By default this rule takes the decision `off`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"root-context-budget": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [root-context-budget]: ...
```

A workspace that sets this rule to `deny` sees `deny [root-context-budget]: ...` instead.

`magus describe rule root-context-budget` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
