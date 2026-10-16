### Added

- **`magus job fork` and `magus describe job` name the files outside a job's write paths
  that use what its write paths define.** The list puts tests first and names the write
  paths each file references, so a test or registry one hop out is added before the job
  starts rather than found red at the end. It is a report and never refuses a fork. `-o
  json` carries it as `referenced_outside`.
