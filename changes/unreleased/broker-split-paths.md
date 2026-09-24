### Changed

- **Breaking: sockets and logs moved.** `$XDG_RUNTIME_DIR/magus/broker.sock` and
  `server.sock` replace `magus-daemon.sock`, and a detached broker or server logs to
  `$XDG_STATE_HOME/magus/`, which survives logout, instead of beside its socket. The
  detached server runs as `magus server --foreground`.
