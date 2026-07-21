---
title: "MGS9005: connector name already exists"
description: Creating a connector token with a name already in use. Connector names are unique; pass a different --name or revoke the existing one first.
tags: [MGS9005, auth, connector, token, create, name]
---

# MGS9005: connector name already exists

`magus config mcp connector create` was asked to mint a token under a name that
the connector store already has. Names are unique so each client's token is
unambiguous to list and revoke.

```text
[MGS9005] magus config mcp connector create: a connector named "ci" already
exists; pass a different --name
  see: .../MGS9005.md
```

## Why

A connector name is the human handle for a token in `list` and `revoke`. Allowing
two tokens with the same name would make it ambiguous which one an operator means
to revoke - a footgun for a credential.

## Resolution

- Pick a different name:

  ```sh
  magus config mcp connector create --name ci-2
  ```

- Or revoke the existing one first if you meant to replace it (there is no renew;
  replacing is revoke-then-mint):

  ```sh
  magus config mcp connector revoke ci
  magus config mcp connector create --name ci
  ```

## See also

- `magus config mcp connector list`: the names already in use.
