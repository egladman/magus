### Added

- **The agent guard refuses a filter that nothing feeds.** A `grep`, `sed`, `jq`, `head`,
  `tr` or similar with no file operand, no pipe into it and no input redirect reads the
  harness's stdin, which can hang past the tool timeout. Each tool's flags are modeled, and
  a call the guard cannot classify passes.
