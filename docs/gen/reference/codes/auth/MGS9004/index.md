---
title: "MGS9004: no auth token configured"
description: A command asked for the operator (cli) token but none exists yet. The daemon mints one on its next start, or you can generate it explicitly.
tags: [MGS9004, auth, token, cli, operator, onboarding]
---

# MGS9004: no auth token configured

A command needed the operator (cli) token - the built-in credential the local
CLI and daemon authenticate with - but none has been created yet.

```text
[MGS9004] no token configured; run `magus config mcp token generate`
  see: .../MGS9004.md
```

## Why

The operator token is auto-seeded the first time the daemon starts. Before that
(a fresh install where the daemon has never run), there is nothing to print or
authenticate with.

## Resolution

Either start the daemon once (it mints the token), or generate it explicitly:

```sh
magus config mcp token generate
```

Then re-run your command.

## What this is NOT

- **Not a connector-token problem.** Connector tokens (for MCP clients) are a
  separate store; this is the built-in operator credential. See MGS9001 for a
  rejected connector token.

## See also

- `magus config mcp token`: print or generate the operator token.
- `magus server start`: starts the daemon, which seeds the token.
