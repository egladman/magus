### Fixed

- **Status, doctor and the console see both background processes.** The liveness probe
  asks the configured server rather than whichever socket a run inherited; doctor's
  `sockets` check reports `broker.sock` and `server.sock` by name; the compact status line
  names each; and the dashboard gains broker and server tiles. `MAGUS_*` settings now
  apply in a workspace with no `magus.yaml`.
