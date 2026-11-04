### Fixed

- **`job exit` compares charm sets.** A ref recording a run under another charm than the
  check resolves to is refused when filed rather than at `job wait`. Exit reads
  `default_charms` from the job's checkout, workspace config only, as the wait does.
