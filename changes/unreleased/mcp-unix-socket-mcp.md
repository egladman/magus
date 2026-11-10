### Added

- **MCP and the Connect APIs on `server.sock`, behind the proc token.** A same-user peer
  sends `server.sock.token` as the `Magus-Proc-Token` header and holds the `socket-peer`
  credential, `mcp=write` and `console=write`, each path held to its loopback need.
  Without the token the socket answers `401`. Every MCP tool call checks `mcp=write`
  again (MGS9015).
