### Fixed

- **`magus self update` never picks a release candidate on its own.** The release
  index lists candidates apart from stable releases, so no shipped binary moves to
  one, and `--version v0.5.0-rc.1` still installs it by name. Cutting a candidate
  records its notes without consuming the changelog fragments the final release
  needs.
