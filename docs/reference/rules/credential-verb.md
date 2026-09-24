---
title: "credential-verb: an agent minting, printing, rotating or revoking a credential through the CLI"
description: "A deny rule: it refuses an agent minting, printing, rotating or revoking a credential through the CLI, and names what to run instead."
tags: [guard, rules, credential-verb, deny]
---

# credential-verb

A deny rule: it refuses an agent minting, printing, rotating or revoking a credential through the CLI, and names what to run instead.

## What it catches

An agent minting, printing, rotating or revoking a credential through the CLI.

## Why

An agent holds the token it was given, and a session that mints another holds a grant nobody handed it. Every command the CLI registry marks as a credential verb is refused: `magus config console token create`, `magus config mcp connector create`, `magus graph export --open --follow` (its link carries a sign-in code), and `magus config token print`, `generate` and `revoke`, the operator token that reaches token management. The list is read from the registry, so a new minting verb is covered by being declared, and it holds however the binary is spelled: `./magus`, a path, `go run ./cmd/magus`, or inside a `$(...)` substitution. This is a seatbelt for a harness that opted in, not a boundary: a process running as the user can reach the same files.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [credential-verb]: ...
```

`magus describe rule credential-verb` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
