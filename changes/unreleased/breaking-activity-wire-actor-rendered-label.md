### Changed

- **BREAKING: the activity wire's `actor` is a rendered label, and `actors` filters by
  field.** `actor` was a kind (`agent`, `operator`); it is now the origin rendered for a row
  head. An `actors` entry matches one origin field exactly (user, host, agent, credential,
  entry point), never the label. The review-remark telemetry label `human` is now
  `unattributed`.
