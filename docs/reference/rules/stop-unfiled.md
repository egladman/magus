---
title: "stop-unfiled: a leased subagent stopping before it filed its job's result"
description: "A deny rule by default: it refuses a leased subagent stopping before it filed its job's result, and names what to run instead."
tags: [guard, rules, stop-unfiled, deny]
---

# stop-unfiled

A deny rule by default: it refuses a leased subagent stopping before it filed its job's result, and names what to run instead.

## What it catches

A leased subagent stopping before it filed its job's result.

## Why

A parent reads what its child did through `magus job wait`, which waits on the row's filed JobResult; a child that stops without one leaves nothing to wait on, and a rule nobody is required to follow is one nobody follows (ADR 0043). At a subagent's stop, when the caller holds a lease whose live row has no result, the stop is refused once, naming `magus job exit <job> --stdin` and `magus job exit --schema`. A read-only row files one too, with an empty changed_paths, so a scout is held to the same rule. The second stop passes with a note, since the host marks it as one a stop rule already held: nothing re-prompts the child, and `magus job wait` shows the row never passed. Where the host's stop hook can block, the refusal blocks the stop, so the child files the record itself. A host that reports no child stop never reaches it. Set to advise, the stop stands and the note says what was not filed.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"stop-unfiled": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [stop-unfiled]: ...
```

A workspace that sets this rule to `advise` sees `advise [stop-unfiled]: ...` instead.

`magus describe rule stop-unfiled` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
