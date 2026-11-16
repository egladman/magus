### Fixed

- **A subagent waiting on its own background run no longer ends its try.** A
  SubagentStop whose agent transcript still shows a live background task leaves the
  try started, end unobserved, on any host version. A handback, `magus job exit` or the
  row's deadline ends it, so the root's stop reads the worker as running, not as stopped
  without a result.
