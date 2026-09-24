### Changed

- **Breaking for SDK callers: the daemon names are gone from the Go API.** `magus.Daemon`,
  `SetDaemon` and `ServeDaemon` are `Server`, `SetServer` and `Serve`;
  `types.EntryPointDaemon`, `HolderDaemon`, `DaemonRequired` and `DaemonSocketWithheld`
  are `EntryPointServer`, `HolderServer`, `ServerRequired` and `ProcSocketWithheld`. The
  `internal/daemon` package is `internal/serverhttp`.
