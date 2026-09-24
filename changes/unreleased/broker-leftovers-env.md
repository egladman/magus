### Changed

- **Breaking: `MAGUS_DAEMON_*` variables are now `MAGUS_SERVER_*`.** `MAGUS_DAEMON_ENABLED`,
  `MAGUS_DAEMON_ADDRESS`, `MAGUS_DAEMON_IDLE_TTL`, `MAGUS_DAEMON_WORKSPACES` and the
  `MAGUS_DAEMON_MAINTENANCE_*` family keep their suffix under `MAGUS_SERVER_`; setting an
  old name is an error that names the new one. The pool pointer magus exports to its
  children, `MAGUS_DAEMON_SOCKET`, is now `MAGUS_PROC_SOCKET`.
