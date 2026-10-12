### Fixed

- **A magusfile naming a guard rule this build predates now reads as a stale binary.** The
  "unknown built-in rule" load failure (MGS1045) is annotated as MGS1021, so state changes
  are denied and the verdict names the rebuild instead of every workspace rule failing
  open. A misspelled rule name, which comes with a suggestion, is not stale.
