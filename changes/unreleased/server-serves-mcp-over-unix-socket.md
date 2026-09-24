### Added

- **`magus server` also serves MCP on `mcp.sock`, no token needed.** On Linux and macOS it
  admits only a peer running as the server's uid, as the `socket-peer` credential holding
  `mcp=write`; others get MGS9022. Every tool call still checks `mcp=write`. `magus server
  status` lists the socket; `mcp.unix_socket: false` turns it off.
