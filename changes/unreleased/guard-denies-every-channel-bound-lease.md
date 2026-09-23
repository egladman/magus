### Changed

- **The guard denies every channel a bound lease could use to rewrite its row** (`magus
  job exec <other>`, `job wait`, `job fork`, `op=clear`). Reads and `--schema` pass.
