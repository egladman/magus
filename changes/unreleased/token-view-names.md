### Fixed

- **A mint or a revoke shows up on the next verification.** The token view's
  signature includes the names in `tokens.d`. A directory timestamp that does not
  move for a second create or unlink in the same coarse tick no longer hides one.
