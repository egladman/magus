### Changed

- **Cursor's harness guards MCP calls and budgets session start.**
  `beforeMCPExecution` fails closed. `sessionStart` sets `PATH` and `__MAGUS_BIN`,
  then injects `magus session --brief` only below 16 KiB. Every hook has a
  10-second timeout. MCP advice and post-compaction rehydration remain unwired.
