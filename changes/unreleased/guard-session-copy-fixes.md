### Fixed

- **A guard dry run keeps its session copy until it finishes, and no copy outlives its
  process.** A dry run nested in a hook judgment that ran past the deadline lost its copy
  while it still read it, and a later write recreated the copy in the temporary directory
  for good. The copy now lasts until the dry run returns. Each copy names the process that
  made it, and the next dry run removes the copy of a process that has exited.
