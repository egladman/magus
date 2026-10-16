### Changed

- **A job record is validated whole before anything is written.** `magus job fork`,
  `magus job apply` and `magus\job.put` report every problem in one pass, each with its
  MGS code, field and fix. A check naming a missing project or target is refused
  (MGS3040), as are uncovered regenerated outputs (MGS3044) and unrun ci targets
  (MGS3045) unless added or waived.
