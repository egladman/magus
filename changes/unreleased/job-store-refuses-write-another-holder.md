### Changed

- **The job store refuses a write to another holder's row.** A leased session may record
  its base, shrink its own `write_paths`, end its own row, and fork inside its paths.
  `clear` archives what it drops.
