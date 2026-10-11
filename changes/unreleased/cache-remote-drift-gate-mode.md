### Fixed

- **A remote drift-gate hit verifies across umasks.** The check compares the executable bit
  only, so a hit recorded as `0644` is no longer refused for a file that is `0664` here.
