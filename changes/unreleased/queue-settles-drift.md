### Changed

- **The queue lands a change whose generated files drifted.** Planning no longer kicks
  back with `KICK_REGENERATION`; validation regenerates the candidate, gates it, and
  uploads it as `candidate.bundle`. Apply runs none of the change's code: it takes that
  commit only once it checks its parent and that it changes declared outputs alone.
