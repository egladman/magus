### Fixed

- **An output descriptor from an older magus no longer hides the output store.** One whose
  `key_version` is still a number reads as a miss with a warning, so `LastFullTest` finds a
  passing `ci` run beside it. `magus x <ref>` on such a ref says the output is from an older magus.
