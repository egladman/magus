### Fixed

- **`magus\job.list` measures overlap footprints.** It left every footprint empty, while
  `magus ls jobs -o json` measured them. Both now measure them the same way.
