### Fixed

- **"Cannot check byte-stability" is recorded.** It fails the gate, and a `-o jsonl` run
  now carries it as `race.determinism_unchecked` with its error.
