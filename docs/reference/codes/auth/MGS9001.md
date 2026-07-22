---
title: "MGS9001: bearer token rejected"
description: The daemon refused a request because its bearer token was missing, wrong, expired, or revoked. This is the usual reason an MCP client cannot connect.
tags: [MGS9001, auth, mcp, connector, token, onboarding, bearer]
---

# MGS9001: bearer token rejected

The daemon received a request on a guarded route (`/mcp`, the console data
services) whose bearer token it would not accept, so it answered `401
unauthorized`. This is the most common reason an MCP client (Claude Code, an
IDE, Desktop) fails to connect.

```text
[MGS9001] the daemon rejected the bearer token: it is missing, wrong, expired,
or revoked. Mint or inspect a connector token with: magus config mcp connector
  see: .../MGS9001.md
```

## Why

Every guarded route requires a bearer token. The daemon does NOT distinguish a
missing token from a wrong one in its reply (that would let a caller enumerate
valid tokens), so any of these produces the same `401`:

- **No token** sent (the client was not configured with one).
- **Wrong token** (a typo, or a token from a different daemon).
- **Expired** connector token (connector tokens can carry an expiry).
- **Revoked** token (revoked from the CLI or the console Settings).

## Resolution

1. Mint a connector token and read how to wire it into your client:

   ```sh
   magus config mcp connector create --name my-client
   ```

2. Confirm the token your client sends matches one the daemon knows:

   ```sh
   magus config mcp connector list
   ```

   A token you expect but do not see was revoked or belongs to another daemon.

3. If it is present but still rejected, it may be expired - remint it (there is
   no renew by design; mint a fresh one).

## What this is NOT

- **Not a network error.** The daemon answered; it declined the token. A refused
  connection or timeout is a different problem (daemon not running, wrong port).
- **Not the operator (cli) token.** That token is managed by the CLI and is not
  the credential an MCP client should carry; use a connector token.

## See also

- `magus config mcp connector`: mint, list, and revoke connector tokens.
