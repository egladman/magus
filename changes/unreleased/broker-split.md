### Changed

- **Breaking: the daemon is two processes, `magus broker` and `magus server`.** A run
  starts the broker, which holds host capacity and shared services on a unix socket only
  and exits after ten minutes holding nothing. Only `magus server start` starts the
  server, which serves MCP, the console and jobs. `server stop --services` is now
  `broker stop --services`.
