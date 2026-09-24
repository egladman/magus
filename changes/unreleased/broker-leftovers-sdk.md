### Changed

- **Breaking for SDK callers: the daemon names are gone from the Go API.** `magus.Daemon`,
  `SetDaemon` and `ServeDaemon` are `Server`, `SetServer` and `Serve`;
  `types.EntryPointDaemon`, `HolderDaemon` and `DaemonSocketWithheld` are
  `EntryPointServer`, `HolderServer` and `ProcSocketWithheld`; `DaemonRequired` is gone. The
  `internal/daemon` package is `internal/serverhttp`.
