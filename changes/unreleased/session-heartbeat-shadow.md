### Added

- **`magus status` reports leased rows whose session went silent.** The guard stamps a
  last-seen time per session and lease, at most once a minute, and status lists a row
  silent past 30 minutes with no background command running. It only measures: nothing is
  ended, released or granted.
