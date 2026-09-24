### Changed

- **Breaking for SDK callers: status types name the broker and the server.**
  `types.StatusSnapshot` gains `Broker`, `Server` and `BrokerPolicy` and drops `Machine`
  and `Services`; `types.StatusOutput` drops `Mode` and renames `DaemonVersion` to
  `Version`. The new `broker` package is the client and server, and
  `workspace.WithMachineAdmitter` is gone.
