### Fixed

- **The server keeps the knowledge graph and symbol indexes current with MCP off.**
  Graph watching and symbol indexing belonged to the MCP listener, so `mcp.enabled:
  false` quietly stopped both; they now run for as long as the server does.
