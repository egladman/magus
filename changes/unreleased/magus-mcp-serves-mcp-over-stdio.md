### Added

- **`magus mcp` serves MCP over stdio.** An agent host registers `{"command": "magus", "args": ["mcp"]}`
  and gets every tool for the workspace it launches in, with no daemon and no token. Each call
  carries a `stdio` credential holding `mcp=write`, and every MCP tool call, over either
  transport, is refused below that with MGS9015.
