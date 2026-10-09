### Added

- **Brief your own model on one review thread.** `magus diff --thread <id>` prints the thread,
  its hunk, and what the symbol index knows about the symbols changed there: reach, callers,
  coverage, conformance findings and anchored notes. `GET /api/v1/diff/thread` and the diff MCP
  tool's `projection: "thread"` return the same brief. magus sends it nowhere.
