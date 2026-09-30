### Added

- **One `client` MCP tool runs the magus module.** A Buzz `main(args: [str])`
  calls `magus\` and the pure host modules, and returns its value as JSON.
  File imports, native FFI, and host modules outside that allowlist are refused
  with MGS3034. Each call runs in its own process.
