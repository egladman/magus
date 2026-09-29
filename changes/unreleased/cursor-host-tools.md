### Fixed

- **Cursor gates Grep, Glob, and Read before they run.** The hook restates
  them as shell and denies on `preToolUse`; `postToolUse` still advises.
  This workspace refuses a read of a run log or task capture, and creating
  or reading a directory named `terminals`.
