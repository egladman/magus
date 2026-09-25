### Added

- **MCP and the Connect APIs on `server.sock`, no token needed.** A same-user peer holds
  the `socket-peer` credential, `mcp=write` and `console=write`, each path held to its
  loopback need. Every MCP tool call, on any transport, checks `mcp=write` again (MGS9015).
