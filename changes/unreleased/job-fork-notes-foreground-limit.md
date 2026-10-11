### Added

- **`magus job fork` notes a check too long to wait on.** When the check's last real run
  took longer than a wired agent host lets a foreground call wait, the fork names both
  durations and suggests a narrower check. A harness declares its limit with the new
  optional `harness_limits` op; JSON output carries it as `foreground_overrun`.
