### Added

- **Every hack script reads its argv with `flags\parse`.** A lint rule reports a script in
  a hack script directory that declares `main` without importing `flags`, so a repeated
  flag, a missing or empty value and `--` mean the same thing in every script.
