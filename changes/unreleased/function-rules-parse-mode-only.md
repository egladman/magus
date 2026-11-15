### Changed

- **Breaking: only the load that discovers the workspace stores a function rule.** A target body
  or pooled worker no longer keeps a session alive, and a registration there for a rule
  the workspace did not register fails with MGS1045 (MGS1052 for `magus\modelTier`).
  `magus\guard.stop` and `step` raise MGS1022 in a spell or `magus buzz` script.
