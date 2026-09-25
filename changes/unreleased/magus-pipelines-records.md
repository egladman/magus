### Added

- **Magus stages in a pipe trade typed records, and projects flow forward.** A run
  whose stdout another magus reads writes its `-o jsonl` records there and its prose
  on stderr, so `magus run format libs/x | magus run lint | magus run test` runs all
  three on libs/x. Named projects still win; an explicit `-o` keeps its format.
