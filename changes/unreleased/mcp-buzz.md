### Added

- **The `buzz` MCP tool transforms JSON with Buzz.** It accepts inline `script` or a
  workspace `path`, an `args` array and an `input` object, then returns the
  JSON value from `transform` and separately captured `std.print` output. The
  forked interpreter has no file imports, native FFI or Magus host modules;
  workspace actions remain with the other Magus MCP tools.
