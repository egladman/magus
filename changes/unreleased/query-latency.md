### Changed

- **`magus query`, `refs` and `explain` answer in a fraction of the time.** Whether a
  symbol index is current used to cost every lookup that could match a symbol a version
  probe of every declared tool, 26 subprocesses on this repository. A stamp beside each
  index now answers that from a stat of the same inputs, and only an index whose inputs
  moved pays the probe. Measured warm here: `query guard` 2.2 s to 0.5 s, `refs
  <symbol> --occurrences` 2.7 s to 0.7 s, `explain <dir>` 3.9 s to 1.3 s. A toolchain
  upgrade with no source change keeps an index reading current until the next `magus
  graph build`.
- **A running `magus server` answers graph reads.** It runs the same stamp-checked build
  a local read runs, so its answer is the local answer. It answers only a client of the
  same build that reads under the same configuration and the same `PATH` and
  index-relevant environment. In every other case, or with no server, the read runs
  locally as before.
