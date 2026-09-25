### Added

- **`magus_buzz` MCP tool runs a Buzz program.** It forks `magus buzz` with inline
  `script` or a workspace `path`, plus `args` and `stdin`, and returns stdout,
  stderr and the exit status, with JSON stdout parsed. A compile or runtime error
  is a tool error. Every call needs `write=true`: `magus buzz` has no read-only mode.
