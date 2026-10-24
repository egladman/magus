### Added

- **`magus job pause` takes `--reason` and `--until`.** A pause records who paused the
  job and why, and lapses at its bound. While it holds, the sweep and `magus job prune
  --all` leave the row alone.
