---
title: "human-only: a magus run reaching a human_only target: the person is asked, a leased worker refused"
description: "A deny rule by default: it refuses a magus run reaching a human_only target: the person is asked, a leased worker refused, and names what to run instead."
tags: [guard, rules, human-only, deny]
---

# human-only

A deny rule by default: it refuses a magus run reaching a human_only target: the person is asked, a leased worker refused, and names what to run instead.

## What it catches

A magus run reaching a human_only target: the person is asked, a leased worker refused.

## Why

Some targets cost what only a person may decide to spend: a benchmark against a paid model API, a suite that drives real hosts on real logins. A magusfile marks one with `human_only` and a reason saying what a run costs. Each `magus run` and `magus affected` on the line is expanded the way the engine would run it: over the projects it names, or every project for `affected` or a run naming none, and down each target's ctx.needs chain. A run naming a human-only target itself, from a session no job lease binds, gets the verdict `ask`: the host's own approval prompt shows the reason, the target's recorded p75 and run count and whether it is cached, and approving runs it once. A hook that cannot put an ask in front of the person refuses instead. A leased worker is refused and nobody is asked: workers do not spend for the orchestrator. A run that reaches one through another target's chain is refused for everyone, naming each human-only target it reaches: a run reporting green over work it skipped is a pass the push gate would trust. A command magus itself suggested is judged too, since the person, not the suggestion, owns the cost. A person's own terminal never meets the guard, so nothing changes there. Set to advise, the reason is told and the run proceeds.

## Default and override

By default this rule takes the decision `deny`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"human-only": "advise"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [human-only]: ...
advise [human-only]: ...
```

`magus describe rule human-only` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
