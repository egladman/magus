### Changed

- **Breaking: `server.sock` speaks HTTP.** Forwarded runs, jobs, status, reload and stop
  are `/proc/v1/` paths on it, and a client meeting a server started by an older magus
  gets MGS3025 naming the restart. On Linux and macOS it admits only processes running
  as the server's user (MGS9022 otherwise).
