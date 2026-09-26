### Fixed

- **magus binds and dials a unix socket at any path length on linux.** A path past
  the 108 bytes `sun_path` holds is reached through its directory under
  `/proc/self/fd`, and the socket stays at its real path. On macOS such a path is
  refused by name, length and limit. The broker, the server and their liveness probes
  all go through this, so a deep `TMPDIR` or cache directory, such as a merge queue
  candidate's, no longer breaks them.
