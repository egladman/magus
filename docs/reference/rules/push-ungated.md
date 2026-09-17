---
title: "push-ungated: a push at a commit with no green gate: the person is asked, a leased worker refused"
description: "A deny rule: it refuses a push at a commit with no green gate: the person is asked, a leased worker refused, and names what to run instead."
tags: [guard, rules, push-ungated, deny]
---

# push-ungated

A deny rule: it refuses a push at a commit with no green gate: the person is asked, a leased worker refused, and names what to run instead.

## What it catches

A push at a commit with no green gate: the person is asked, a leased worker refused.

## Why

The advisory this replaced fired on EVERY push, having read nothing: it told a caller who had just gated and a caller who had never gated the same sentence, which is a toll rather than a reminder. This one reads the run log, so the finding is a fact: which invocations ran the gate, which commit each was built from, and how it finished. That is what makes stopping the call legitimate here where the rest of this tier only advises. It matches on the COMMIT and not the exact tree, deliberately: an exact match would expire on the first comment typo after a green run, which is the delta the cadence already says to push, and a rule that fires there is one people route around. Publishing work in progress is legitimate and indistinguishable from an oversight, so a session no job lease binds gets the verdict `ask`: the host's own approval prompt puts the push in front of the person, and approving it publishes. A marker the agent types is not consent, so nothing it says clears this. A session bound to a lease is a worker, and workers do not publish: it gets `deny`, and nobody is asked.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [push-ungated]: ...
```

`magus describe rule push-ungated` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
