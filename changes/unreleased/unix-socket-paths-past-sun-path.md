### Fixed

- **magus binds and dials a unix socket at any path length on linux.** A path past
  the 108 bytes `sun_path` holds goes through its directory under `/proc/self/fd`.
  macOS refuses such a path, naming its length and the limit. The broker and server
  use this, so a deep `TMPDIR`, such as a merge queue candidate's, no longer breaks
  them.
