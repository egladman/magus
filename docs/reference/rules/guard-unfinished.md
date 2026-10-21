---
title: "guard-unfinished: guard checks that never finished, so no rule judged their calls"
description: "An advisory by default: it explains, and blocks nothing, on guard checks that never finished, so no rule judged their calls."
tags: [guard, rules, guard-unfinished, advise]
---

# guard-unfinished

An advisory by default: it explains, and blocks nothing, on guard checks that never finished, so no rule judged their calls.

## What it catches

Guard checks that never finished, so no rule judged their calls.

## Why

A host stops waiting on a hook that has not answered within its hook timeout, with no trail row and no word to the agent. So a check that has not answered within the guard's own budget, 7s against the host's 10s, stops waiting on its rules and answers unjudged: a call that changes state is denied, to be retried, and one that only reads runs with a note saying no rule judged it. The trail records the check as lapsed. Each check leaves a mark when it starts, before the workspace's rules load, and removes it when it answers while the host still waits. The host stopped waiting when the hook process it started is gone, or past the hook timeout the host's harness declares in mgs_getLimits; a host whose harness declares none is not taken to have given up on a slow check. The next check in the session reports and clears every mark whose process ended without removing it, and every one still running past the declared timeout. It stands alone only when the rules passed the call; on any other verdict the note is appended to that verdict. A workspace cannot set it: magus\guard.builtins refuses the name, since the notice is how the workspace learns a guard check of its own judged nothing.

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
