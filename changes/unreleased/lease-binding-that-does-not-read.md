### Changed

- **A lease binding that does not read is an error.** A marker holding anything but a lease
  id no longer reads as unbound: the guard denies with the path, the CLI commands that
  resolve a lease fail, and `magus job exec --vacate` clears it. The guard lets that
  command and help through, so an agent can recover.
