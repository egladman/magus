### Added

- **`local-path`, a proofread house rule.** It reports a path only the author's machine
  holds, such as `~/...`, `/Users/<name>/...`, `$HOME/...`, a temp directory or a drive
  letter, in a pull request, commit, issue, changelog or review. URLs, link targets and
  `/dev/null` pass. It is off by default, and this repository denies it.
