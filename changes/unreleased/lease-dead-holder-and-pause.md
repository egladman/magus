### Added

- **A dead holder's path goes to the job that needs it.** When a write lands in a path
  whose holder's session ended, or went silent past 30 minutes with no background command
  running, magus claims it for the writer with a `dead:<job>` proof, and both rows record
  who held it and who took it.
