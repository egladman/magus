### Fixed

- **The queue merges a gated candidate whose only regeneration was a settled conflict.**
  When validation's regeneration wrote nothing, apply kicked the change with
  `KICK_REGENERATION`; it now merges its own rebuild once that rebuild is the commit
  validation gated, still running none of the change's code.
