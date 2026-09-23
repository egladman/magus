---
title: "operator-token: an agent reading or rotating the operator token"
description: "A deny rule: it refuses an agent reading or rotating the operator token, and names what to run instead."
tags: [guard, rules, operator-token, deny]
---

# operator-token

A deny rule: it refuses an agent reading or rotating the operator token, and names what to run instead.

## What it catches

An agent reading or rotating the operator token.

## Why

The operator token holds every surface on loopback, token management included, so whoever holds it can mint a token with any grant. `magus config token print` and `generate` are refused however they are spelled, a `$(...)` substitution included. An agent holds its own token, minted for it with `magus config mcp connector create`; a console link carries a short-lived console token from `magus config console token create`.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [operator-token]: ...
```

`magus describe rule operator-token` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
