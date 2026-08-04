---
title: auth diagnostics
page_type: overview
description: Landing page for MGS9xxx diagnostics covering the daemon's bearer-token auth - the operator (cli) token and connector tokens minted for MCP clients.
tags: [auth, diagnostics, error codes, MGS9xxx, mcp, connector, token, bearer]
---

# Auth diagnostics

Codes in the `MGS9xxx` range flag problems with the daemon's bearer-token auth:
the operator (cli) token the local CLI and daemon share, and the named
connector tokens minted for external [MCP](../../../guides/integrations/mcp.md)
clients (Claude Desktop, an IDE, Codex). Magus raises them at run time as a
typed `DiagnosticError`, most often from `magus config mcp token` or `magus
config mcp connector`.

## Codes

- [MGS9001](MGS9001.md): the daemon rejected a bearer token - missing, wrong, expired, or revoked.
- [MGS9002](MGS9002.md): a token file has insecure permissions and magus refuses to load it.
- [MGS9003](MGS9003.md): the connector store was written by a newer magus than the one reading it.
- [MGS9004](MGS9004.md): no operator (cli) token has been created yet.
- [MGS9005](MGS9005.md): a connector name is already in use.
- [MGS9006](MGS9006.md): a connector revoke or lookup matched no stored token.
