### Added

- **The broker can run under systemd or launchd.** `magus broker units` prints the units;
  magus never installs them. Under systemd the broker serves the socket the supervisor
  hands over and refuses a malformed handover. `magus broker --idle-exit 0` keeps it up
  for a supervisor. Its wire is versioned, and a new broker answers the previous version.
