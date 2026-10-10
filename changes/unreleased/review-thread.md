### Added

- **`magus diff --thread <id>` narrows the review to one pull request thread.** The viewer opens
  on its hunk; printed, it is the conversation, the hunk, and what the change there reaches.
  While the server runs, the report lists each thread's id beside its hunk; a report never
  asks the host itself, so with no server it lists none and says how to start one. `-o json`, `GET /api/v1/diff/thread` and
  the diff MCP tool's read-only `thread` op return the same record.
