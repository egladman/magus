### Changed

- **An older magus reads a job store a newer one wrote.** A row from a newer build no
  longer locks every older binary out of the whole store: its unknown fields and version
  survive a rewrite, and only a row declaring a requirement this build lacks turns read-only,
  named in `magus ls jobs`. Adding a field no longer bumps the schema version.
