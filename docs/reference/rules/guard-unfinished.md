---
title: "guard-unfinished: earlier guard checks that never finished, so the host ran their calls with no rule applied"
description: "An advisory by default: it explains, and blocks nothing, on earlier guard checks that never finished, so the host ran their calls with no rule applied."
tags: [guard, rules, guard-unfinished, advise]
---

# guard-unfinished

An advisory by default: it explains, and blocks nothing, on earlier guard checks that never finished, so the host ran their calls with no rule applied.

## What it catches

Earlier guard checks that never finished, so the host ran their calls with no rule applied.

## Why

The host kills a hook that has not answered in ten seconds and runs the call unjudged, with no trail row and no word to the agent. Each check leaves a mark when it starts, before the workspace's rules load, and removes it when it answers inside that time; the next check in the session reports and clears every mark older than fifteen seconds. It stands alone only when the rules passed the call; on any other verdict the note is appended to that verdict. A workspace cannot set it: magus\guard.builtins refuses the name, since the notice is how the workspace learns a guard check of its own judged nothing.

## Default and override

This rule always takes the decision `advise`. A workspace cannot set it:
naming it in magus\guard.builtins fails to load, because the rule is how a workspace
learns that a rule of its own judged nothing.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [guard-unfinished]: ...
```

`magus describe rule guard-unfinished` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
