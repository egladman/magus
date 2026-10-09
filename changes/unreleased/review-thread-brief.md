### Added

- **Brief your own model on one review thread.** `magus diff --thread <id>` prints the
  whole thread oldest first, the hunk it sits in (the host's copy when the comment is
  outdated), and what the symbol index knows about the symbols changed in that hunk: how many
  files reference them, which projects they are public to, the conformance findings, the
  coverage and the callers that cross a boundary. It adds the notes anchored to the file and one
  line on the change as a whole. The id is the thread id, the id of its first comment, or any reply's. `GET
  /api/v1/diff/thread?id=` and the diff MCP tool's `state` op with `projection: "thread"` and
  `thread` return the same text as `{id, brief}`. The brief asks for findings and leaves the
  reply to you: magus sends it nowhere, the thread is marked as other people's words, and
  nothing in it is a reply you could paste.
- **An agent can leave an outline of a thread for you to read.** The diff MCP tool takes
  an `outline` op with `thread` and `topics`: at most five topics of at most 60 characters,
  one line each, and anything longer is refused with a message that you type the reply. It is
  held in memory with the session, never persisted, and shows on `DiffReview.outlines` for the
  console to render without a way to copy or send it. No MCP op replies to, publishes or
  approves a review.

### Changed

- **The note-anchor join behind `magus diff --impact` now lives in `internal/review`**, with the
  changed-path and changed-symbol lists it joins against, so the brief and the report read one
  definition. The report is unchanged.
