### Added

- **`mcp.http` (`MAGUS_MCP_HTTP`) takes `/mcp` off the HTTP listener.** With
  `mcp.http: false` the server mounts no `/mcp` route on loopback, while `magus mcp`
  (stdio) and `/mcp` on the server socket keep serving, and the listener keeps the console
  and the health routes. `mcp.http: true` beside `mcp.enabled: false` is a config error.
