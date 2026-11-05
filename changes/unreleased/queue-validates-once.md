### Fixed

- **The merge queue's own labels no longer start a second validation.** On a `labeled`
  event, only a merge method's label is merge intent. The queue's `queue: queued` mark on
  a pull request that already carried one started a follow-up run that validated the same
  head again. Dispatch now decides from the action and label the intent job's name carries.
- **Queue apply follows a validation that runs past an hour.** It lists and downloads
  artifacts with the job's own token, and mints the app's one-hour token only once the
  first verdict lands, for the writes. Known limit: a verdict landing more than 60 minutes
  after the first meets an expired write token.
