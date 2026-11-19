### Changed

- **Breaking: each project root holds its own `analyzers.yaml`.** `hack/lint/analyzers.yaml`
  is gone. `no-throw` judges a file against its own project's `analyzers.yaml`, with paths
  relative to that project, and `accessorclone` reads the one at the root of the module it
  lints. Neither falls back to another project's file; a project with none gets the strict
  defaults.
- **Breaking for SDK callers: `accessorclone.Options` drops `Policy`.** The analyzer reads
  `analyzers.yaml` from its module's root, and a missing file or key excuses nothing.
