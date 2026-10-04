### Changed

- **`magus session --brief` claims host capacity before it Inspects.** It starts the
  broker (when enabled) and holds a declared `session-brief` seat for the workspace
  load, so a stack of Cursor `sessionStart` hooks cannot outspend the machine the way
  a stack of runs cannot. A full budget refuses with exit 75; the Cursor hook already
  treats a failed brief as env-only.
