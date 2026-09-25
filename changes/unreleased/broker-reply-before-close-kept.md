### Fixed

- **A broker reply that arrives just before the connection closes is no longer lost.**
  `magus broker stop` could report `connection closed during shutdown` after the broker
  had answered and stopped.
