### Added

- **`magus diff --thread <id>` narrows the review to one pull request thread.** The viewer opens
  on its hunk; printed, it is the conversation, the hunk, and what the change there reaches.
  The report lists each thread's id beside its hunk. `-o json`, `GET /api/v1/diff/thread` and
  the diff MCP tool's read-only `thread` op return the same record.
