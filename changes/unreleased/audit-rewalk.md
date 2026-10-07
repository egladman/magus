### Fixed

- **MGS3001 no longer reports writes a run did not make.** A Ctrl+C or an unreadable
  directory cut the audit's re-walk short, and every file it never reached read as
  removed. The re-walk now finishes after a cancellation, so a real write is still
  reported, and an unreadable directory is reported itself.
