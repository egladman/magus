### Added

- **`magus diff --thread <id>` narrows the review to one pull request thread.** The viewer opens
  on its hunk; printed, it is the conversation, the hunk, and what the change reaches. While the
  daemon runs, reports list thread ids beside their hunks; without it a report never asks the
  host. The diff MCP tool's `thread` op returns the same record.
