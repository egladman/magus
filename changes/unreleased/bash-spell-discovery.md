### Added

- **The bash spell reports where shell scripts live.** Its `discover_projects` claims
  every directory holding a `*.sh` or `*.bash` file as a `member`, never a project, so
  bash binds to whichever project owns each script and adds `shellcheck` to its `lint`.
  A `.shellcheckrc` with no script at or below it is reported as a gap.
