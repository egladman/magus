### Fixed

- **Queue apply follows a validation that runs past an hour.** It waits and lists
  artifacts on the job's own token, `MERGEQUEUE_ARTIFACTS_TOKEN`, which `list_artifacts`
  now requires. The waits send conditional requests and retry a 5xx or a rate limit.
  Known limit: a verdict landing more than 60 minutes after the first meets an expired
  write token.
