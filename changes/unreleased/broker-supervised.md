### Added

- **The broker can run under systemd or launchd.** `magus broker units` prints the units;
  magus never installs them. Under systemd the broker serves the socket the supervisor
  hands over and refuses a malformed one or one bound anywhere but `broker.sock`.
  `magus broker --idle-exit 0` keeps it up for a supervisor.
