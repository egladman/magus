### Added

- **The spell contract has a reference page.** `docs/reference/spell-contract.md` lists
  every `mgs_` function a spell may export with the exact return type magus expects,
  generated from the contract magus loads spells against. MGS1051 names a spell whose
  `mgs_` functions do not match it; magus does not raise it on load yet.
