### Added

- **One `client` MCP tool runs the magus module.** A Buzz `main(args: [str])`
  calls `magus\` and the pure host modules, and returns its value as JSON.
  File imports, native FFI, and host modules outside that allowlist are refused
  with MGS3034. Each call runs in its own process.
- **`client` declares MCP task support.** Bounded at 10 minutes when called
  directly; a host that supports MCP tasks can run it as a task without that
  bound, and cancels it with `tasks/cancel`.

### Removed

- **Per-verb MCP tools the magus module already covers.** `describe`,
  `describe_file`, `insight`, `run_target`, `doctor`, `memory`, `query`,
  `output`, `explain`, `refs`, `path`, `stats`, `vcs_checkpoint`, and `job`
  are gone, and so are `where`, `run_affected`, `affected_plan`, and
  `affected_explain`. Call the matching `magus\` member from `client`. The
  client does not offer `magus\cmd` or `magus\pry` (a magusfile and
  `magus buzz` still do).
  Tools with no matching member stay: `buzz`,
  `status`, `config`, `console`, and `diff`.

### Changed

- **BREAKING: the remaining MCP tools drop the `magus_` prefix.** They are
  `buzz`, `status`, `config`, `console`, and `diff`; hosts already namespace a
  tool by its server.
