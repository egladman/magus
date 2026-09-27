### Fixed

- **The broker adopts a supervisor's socket bound past the 107-byte path limit.** A socket
  bound through its directory reported a closed file descriptor's name, so the broker
  refused it; it now asks the kernel which file the socket is bound to.
