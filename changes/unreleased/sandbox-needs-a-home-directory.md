### Changed

- **A sandbox policy needs a home directory.** Building one, or resolving a spell's cache
  directories, now fails when the home directory cannot be found instead of granting
  against an empty one. A ref lookup that cannot read the session store logs a warning
  that other checkouts were not consulted.
