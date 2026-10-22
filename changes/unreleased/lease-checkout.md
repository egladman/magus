### Added

- **A worker holding a checkout can no longer run magus outside it.** The guard places
  each magus call by its line (`cd`, `-C`, `--root`) and the new `lease-checkout` rule
  denies a call resolving outside the row's checkout, quoting `<root>/magus -C <root> ...`.
  Lease rules now judge the integrator and the row's check where the line runs.
