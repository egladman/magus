### Changed

- **`lease-checkout` names one path and passes `describe`.** The refusal quotes
  `cd <root> && ./magus <args>`, and `magus describe` runs from any directory.
- **The goal lint line prints a count.** `job fork` says `added the goal "lint" (58 files)`
  in place of the whole check; `magus describe job` still holds it.
