### Changed

- **Breaking: `mgs_getCoverage` declares `{format, profile}`, with no alias.** The
  target that declares the profile as an output writes it, and change risk reads it
  from that target's cache entry. The reserved `coverage` op, `MAGUS_COVERAGE_DIR` and
  the old `command`, `file`, `test` and `uses` fields are removed; a spell declaring
  them fails to load.
