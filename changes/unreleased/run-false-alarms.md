### Fixed

- **MGS3001 no longer reports writes a run did not make.** An audit re-walk cut short by
  Ctrl+C or an unreadable directory read every file it never reached as removed, so a
  target that wrote nothing was blamed for emptying a nested project. An interrupted step
  now reports its cancellation, and an unreadable tree skips the audit with a warning.
