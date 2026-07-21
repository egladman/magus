---
title: "MGS9006: connector token not found"
description: Revoking or looking up a connector token by a name or fingerprint that matches nothing in the store.
tags: [MGS9006, auth, connector, token, revoke, fingerprint]
---

# MGS9006: connector token not found

`magus config mcp connector revoke` (or a lookup) named a connector token - by
name or fingerprint - that the store does not contain.

```text
[MGS9006] magus config mcp connector revoke: no connector matches "ci"
  see: .../MGS9006.md
```

## Why

Revoke resolves the token you named against the store. Nothing matched: the name
or fingerprint is wrong, it was already revoked, or it belongs to a different
daemon's store.

## Resolution

List what is actually there and revoke by an exact name or fingerprint from the
list:

```sh
magus config mcp connector list
magus config mcp connector revoke <name-or-fingerprint>
```

A fingerprint prefix works if it is unambiguous; if it matches more than one
token, magus asks you to disambiguate rather than guess.

## What this is NOT

- **Not the operator token.** The built-in cli token is not in the connector
  store and is never a revoke target here.

## See also

- `magus config mcp connector list`: the tokens you can revoke.
