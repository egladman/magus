### Changed

- **A running `magus server` answers graph reads.** It runs the same stamp-checked build
  a local read runs, so its answer is the local answer. It answers only a client of the
  same build that reads under the same configuration and the same `PATH` and
  index-relevant environment. In every other case, or with no server, the read runs
  locally as before.
