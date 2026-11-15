### Changed

- **A function rule is stored by the load that discovers the workspace and by no
  other.** A target body or a pooled worker that runs the root magusfile again no
  longer keeps its session alive, and a registration made there for a rule the
  workspace did not register is refused with MGS1045 (MGS1052 for
  `magus\modelTier`). `magus\guard.stop` and `magus\guard.step` raise MGS1022 in a
  spell or a `magus buzz` script, as the other registering members do.
