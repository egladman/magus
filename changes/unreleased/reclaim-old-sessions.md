### Fixed

- **A session file from before the session group rename no longer stops the disk reclaim.**
  The reclaim removes a file stamped `magus.sessions/v1alpha1`, which this build cannot
  read, warns once with its path, and carries on.
