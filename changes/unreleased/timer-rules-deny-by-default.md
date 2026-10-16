### Changed

- **The guard's timer rules deny by default.** `sleep-poll`, `busy-wait` and
  `process-poll` used to advise, so a sleep timer passed whenever the workspace policy
  failed to load or a judgment ran out of time. They now refuse in those cases too. Deny
  is the right default for every magus user, not only this repository: a background task
  already notifies the agent when it exits, so a timer or a poll adds no information and
  only spends a turn. A workspace that wants the old behavior sets the rule to `advise`
  or `off` in `magus\guard.builtins`.
