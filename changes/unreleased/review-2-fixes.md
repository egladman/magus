### Fixed

- **The guard judges a job's lines by what the checkout holds.** A refused `job exec` binds
  nobody; a row this build cannot read passes only lines a binary can read; and a script
  behind an unreadable `cd` no longer judges from the wrong directory.
