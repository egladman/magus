### Fixed

- **`magus describe harness` prints what a current harness wires.** Text output lists each
  managed hook's matcher and command, and `-o json` carries them under `wired`, whether or
  not anything is left to merge.
