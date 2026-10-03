### Changed

- **Cursor's harness guards MCP calls and budgets session start.**
  `beforeMCPExecution` fails closed. `sessionStart` sets `PATH` and `__MAGUS_BIN`,
  then injects `magus session --brief` only below 16 KiB. Every hook has a
  10-second timeout. MCP advice and post-compaction rehydration remain unwired.

- **`magus session --brief` caps live leases it names.** Editing jobs come first;
  at most 12 live leases are printed, with an `and N more: magus ls jobs` line for
  the rest, so a dirty store of exited holders cannot refill a compacted window.

- **`magus session --brief` claims host capacity before it Inspects.** It starts the
  broker (when enabled) and holds a declared `session-brief` seat for the workspace
  load, so a stack of Cursor `sessionStart` hooks cannot outspend the machine the way
  a stack of runs cannot. A full budget refuses with exit 75; the Cursor hook already
  treats a failed brief as env-only.
