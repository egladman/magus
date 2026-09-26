### Fixed

- **The queue no longer kicks back a change whose only regeneration was a settled
  conflict.** If a change edits its own generator and conflicts with the base in a
  generated file, and validation's regeneration writes nothing, validation uploads no
  bundle. Apply used to kick such a change with `KICK_REGENERATION`. It now merges its
  own rebuild of the candidate once that rebuild is the commit validation gated, and it
  still runs none of the change's code.
