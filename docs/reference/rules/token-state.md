---
title: "token-state: an agent reading or writing the token secrets: the operator token file or the token store"
description: "A deny rule: it refuses an agent reading or writing the token secrets: the operator token file or the token store, and names what to run instead."
tags: [guard, rules, token-state, deny]
---

# token-state

A deny rule: it refuses an agent reading or writing the token secrets: the operator token file or the token store, and names what to run instead.

## What it catches

An agent reading or writing the token secrets: the operator token file or the token store.

## Why

The operator token file (`magus/mcp_token` in the user state dir) and the token store (`magus/tokens.d`) are the credentials the server checks, so reading one hands a session a grant and writing one mints a token. Refused on both graded surfaces: an editor write aimed at them, and any shell line that names them, whatever the command (`cat`, `cp`, a redirect, an interpreter's inline script). A path is matched by name anywhere in a word and by resolving it against where the call runs. A bare listing passes (`ls`, `du`, `stat`, `test` of the state dir or a token file), alone or piped into a text filter such as `head` or `grep`: it shows file names, and none is a secret, since the operator file is always `mcp_token` and a store entry is `<token name>.json`, the name `magus config mcp connector ls` already prints. A listing inside a substitution, or piped into anything else (`| xargs cat`), is refused like any mention. Reads through a host's read tool are not graded: that hook only records, by contract. This is a seatbelt, not a boundary against a process running as the user.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
deny [token-state]: ...
```

`magus describe rule token-state` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
