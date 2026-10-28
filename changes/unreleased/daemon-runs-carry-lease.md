### Fixed

- **A run the daemon executes is charged to the lease its checkout binds.** Every
  workspace the daemon served shared one broker connection, so its runs read `unleased`
  in `magus status` and no job's weight or `jobs.max_validations` applied to them. A
  workspace whose checkout binds a lease now opens with a connection of its own naming
  that lease, reopens when the binding moves, and an unleased run says so.
