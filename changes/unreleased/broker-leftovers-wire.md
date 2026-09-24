### Changed

- **Breaking: the wire says server where it said daemon.** `Pool.daemon_version` is
  reserved and replaced by `owner_version`; `JOB_HOLDER_DAEMON` is reserved and replaced by
  `JOB_HOLDER_SERVER`; the entry point recorded on an event is `server`, not `daemon`; and
  the doctor check `daemon-version` is `server-version`.
