### Fixed

- **A hint is shown once per invocation, not once per server lifetime.** `magus server`
  teaches each command it serves again, and an MCP tool call gets its own set.
