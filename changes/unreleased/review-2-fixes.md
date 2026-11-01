### Fixed

- **The guard judges a job's lines by what the checkout holds.** A `job exec` that would be refused
  binds nobody, a row this build cannot read passes only the lines that get a binary able to read
  it, and a script behind an unreadable `cd` no longer reads its relative calls and commits from
  the wrong directory. A too-old binary names `./magus` only when that is a different binary.
