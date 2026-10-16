### Added

- **A job holding paths another fork was refused for is told so.** The
  `release-requested` advisory reads the open release requests filed for the session's
  lease and says, once a session, which fork wanted what, the write paths the job has not
  used, and the `magus job apply -f <record>` that narrows it. A session with no lease
  never sees it.
