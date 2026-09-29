### Changed

- **Agent hosts get their own MCP server.** `magus describe harness` registers a
  per-session stdio `./magus mcp` in the checkout, which keeps its graph warm. Every
  tool result names the served root and build under `_meta.magus`, and adds a line
  when the caller's roots or the rebuilt binary differ.
