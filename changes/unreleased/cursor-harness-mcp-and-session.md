### Changed

- **Cursor's harness gates MCP calls and starts a session with a budgeted brief.**
  `.cursor/hooks.json` wires `beforeMCPExecution` (fail-closed) and `sessionStart`
  (sets `PATH` / `__MAGUS_BIN`; injects `magus session --brief` only when under
  `SESSION_BRIEF_MAX_BYTES`; every entry has `"timeout": 10`). Profile a heavy
  hook with `MAGUS_PPROF=mem:...` and `magus buzz --profile`: the outer Buzz
  compile is milliseconds; the nested brief pays the workspace load. MCP advise
  and post-compaction rehydrate stay host limits.

- **`magus session --brief` caps live leases it names.** Editing jobs come first;
  at most 12 live leases are printed, with an `and N more: magus ls jobs` line for
  the rest, so a dirty store of exited holders cannot refill a compacted window.

- **`magus session --brief` claims host capacity before it Inspects.** It starts the
  broker (when enabled) and holds a declared `session-brief` seat for the workspace
  load, so a stack of Cursor `sessionStart` hooks cannot outspend the machine the way
  a stack of runs cannot. A full budget refuses with exit 75; the Cursor hook already
  treats a failed brief as env-only.
