### Changed

- **A tool that runs but cannot say which build it is now fails the run as MGS3035.** The
  cache key used to record `UNPROBED` for it. An absent tool still keys as `UNPROBED` with
  a warning. Graph reads do not fail: `magus status` reports that project's symbol index
  as `unvouched`, with the reason.
