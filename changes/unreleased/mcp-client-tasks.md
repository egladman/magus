### Added

- **`client` declares MCP task support.** Bounded at 10 minutes when called
  directly; a host that supports MCP tasks can run it as a task without that
  bound, and cancels it with `tasks/cancel`.
