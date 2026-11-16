### Fixed

- **`magus buzz -t` records its run when the job store cannot be read.** It exits as the tests did, prints the store's error, and says the run was recorded without a lease.
- **A job store refusal names the store file to move aside.**
- **The guard denies a call when the tool rows installed for its host carry another magus's stamp.** It names `magus agent harness install`; before, it logged a warning and judged without them.
