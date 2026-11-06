### Added

- **`magus run -o json` and `magus affected -o json` give each project's status.** A
  project entry carries `status`, the journal's word for the asked target's result: `pass`,
  `cached` or `permitted`. A target that passed only by a deviation ledger reads
  `permitted`, never `pass`. A dry run carries none, and `magus x` carries none because it
  records no journal.
