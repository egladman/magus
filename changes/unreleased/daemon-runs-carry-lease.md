### Fixed

- **A run the daemon executes is charged to the lease its checkout binds.** The daemon
  shared one broker connection across workspaces, so its runs read `unleased` and no
  job's limits applied. A workspace whose checkout binds a lease now opens its own
  connection naming that lease.
