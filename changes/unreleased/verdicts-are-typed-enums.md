### Changed

- **An unknown decision name is refused where it is read.** A `magus\guard.builtins`
  decision, a merge queue verdict's decision and a proofread decisions table now reject a
  name outside their set as they decode it, naming the value and the names they take. A
  built-in setting that gives no decision says so instead of quoting an empty one.
