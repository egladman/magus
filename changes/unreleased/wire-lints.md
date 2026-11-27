### Changed

- **Three repository lints now run.** `library-imports-stay-inside` and
  `core-reads-no-analyzer-policy` join `analyzers.buzz`. `stubs-never-reach-main` moves into
  `libs/analyzer`, takes its extensions from the loaded spells, and refuses rather than advises.
