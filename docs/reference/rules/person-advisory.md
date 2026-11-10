---
title: "person-advisory: a root session ending its turn with an open `magus session advise` advisory it has not been shown"
description: "A deny rule by default: it refuses a root session ending its turn with an open `magus session advise` advisory it has not been shown, and names what to run instead."
tags: [guard, rules, person-advisory, deny]
---

# person-advisory

A deny rule by default: it refuses a root session ending its turn with an open `magus session advise` advisory it has not been shown, and names what to run instead.

## What it catches

A root session ending its turn with an open `magus session advise` advisory it has not been shown.

## Why

An advisory is a person's directive to every session in the repository, and a stop is the one moment every host hands text back to the model. So a root session's stop that nothing else refuses is held once per advisory, with the advisory's text as the reason, and the session keeps working with it in front of it. Where the host renders a prompt's context (Claude Code, Codex) the next prompt shows it first and the stop does not repeat it. A subagent's stop shows nothing, so a child cannot spend its root's one showing. Nothing closes an advisory but `magus session retract <id>`, which is a person's to run. The rule is fixed: a workspace setting would let a checkout silence the person.

## Default and override

This rule always takes the decision `deny`. A workspace cannot set it:
naming it in magus\guard.builtins fails to load, because the rule is how a workspace
learns that a rule of its own judged nothing.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [person-advisory]: ...
```

`magus describe rule person-advisory` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
