### Changed

- **The guard's timer rules deny by default.** `sleep-poll`, `busy-wait` and
  `process-poll` used to advise, so a sleep timer passed whenever the policy failed to
  load or a judgment timed out. A background task already notifies the agent when it
  exits, so a timer adds nothing. A workspace can set a rule to `advise` or `off` in
  `magus\guard.builtins`.
