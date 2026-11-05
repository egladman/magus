### Fixed

- **Queue apply follows a validation that runs past an hour.** It lists and downloads
  artifacts with the job's own token, and mints the app's one-hour token only once the
  first verdict lands, for the writes. Known limit: a verdict landing more than 60 minutes
  after the first meets an expired write token.
