### Changed

- **proofread spells its flags the way magus does.** `-o` picks the output format in
  place of `-format`, `voice build` writes to `--out` in place of `-o`, and a kind read
  from stdin needs `--stdin`: `proofread commit-message --stdin < msg`, where it read
  stdin silently before. Help and errors spell long flags `--name`; the single-dash
  spelling still parses.
