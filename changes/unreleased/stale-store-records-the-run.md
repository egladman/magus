### Fixed

- `magus buzz -t` records its run when the job store cannot be read, and exits as the tests did. It
  prints the store's error and says the run was recorded without a lease.
- A job store refusal names the store file to move aside.
- The guard denies a call, and names `magus agent harness install`, when the tool rows installed for
  its host carry another magus's stamp, where it logged a warning and judged without them.
