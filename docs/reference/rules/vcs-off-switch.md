---
title: "vcs-off-switch: an agent's write setting vcs.enabled: false in a magus.yaml this workspace reads"
description: "A deny rule: it refuses an agent's write setting vcs.enabled: false in a magus.yaml this workspace reads, and names what to run instead."
tags: [guard, rules, vcs-off-switch, deny]
---

# vcs-off-switch

A deny rule: it refuses an agent's write setting vcs.enabled: false in a magus.yaml this workspace reads, and names what to run instead.

## What it catches

An agent's write setting vcs.enabled: false in a magus.yaml this workspace reads.

## Why

With vcs off, vcs.Resolve returns no VCS, so the guard has no approved copy to compare a policy edit against and every workspace rule is read from the working tree alone. Decided by parsing the proposed magus.yaml content, never by matching text. A person editing their own checkout, with no lease and no spawn ancestry, is untouched.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [vcs-off-switch]: ...
```

`magus describe rule vcs-off-switch` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
