### Fixed

- **A guard dry run keeps its session copy until it finishes.** A dry run nested in a hook
  judgment that ran past the deadline lost its copy while it still read it, and a later
  write recreated the copy in the temporary directory for good. The copy now lasts until
  the dry run returns.
