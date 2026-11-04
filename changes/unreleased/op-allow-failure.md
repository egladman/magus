### Added

- **A captured op can report a non-zero exit.** `{"allowFailure": true}` in an op call's
  options returns `{stdout, stderr, code, ok}` when the process exits non-zero, as
  `proc\exec` does. A process that never started and a cancelled run stay errors, and an
  op that does not capture refuses the option.
