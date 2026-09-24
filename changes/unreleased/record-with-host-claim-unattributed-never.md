### Changed

- **A record with no host claim is unattributed, never "a person".** `magus shell` typed at
  a terminal records `entry_point: cli`, no session, and no longer `actor: "agent"`; its
  terminal window keys fire-once notices but is not recorded as a session. The OS user
  says whose account acted.
