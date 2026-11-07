### Added

- **`magus diff` reads the riskiest files first and says why.** Each file carries the tier
  `magus affected ci` proves for it (`files[].risk`), and the reading order puts what magus
  could not bound before what it proved mechanical. Each step prints a `risk:` line, and
  nothing new is folded.
