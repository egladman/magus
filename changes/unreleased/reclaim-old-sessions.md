### Fixed

- **A session file from before the session group rename no longer stops the disk reclaim.**
  The reclaim renames a file stamped `magus.sessions/v1alpha1`, which this build cannot
  read, to `<id>.retired-magus.sessions` beside it, warns once with the new path, and
  carries on. Nothing is deleted.
