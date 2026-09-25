### Added

- **A write path can claim one declaration of a file.** `run.go#executeStages` lets two
  jobs start on one file: claims on different declarations do not overlap, `magus ls jobs`
  lists the pair as `claims: disjoint`, and dropping a claim releases that declaration's
  own lines. `magus job fork` refuses a claim nothing can grade (MGS3031).
