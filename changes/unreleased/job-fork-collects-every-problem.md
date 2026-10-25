### Changed

- **A job record is validated whole before anything is written.** `magus job fork` and
  `apply` list every problem at once, each with its MGS code, field and fix. A bad field is
  MGS3037, an ungradable goal MGS3038, an unrunnable check MGS3040. Uncovered regenerated
  outputs (MGS3044) and ci targets over them that nothing runs (MGS3045) are refused unless
  added or permitted.
