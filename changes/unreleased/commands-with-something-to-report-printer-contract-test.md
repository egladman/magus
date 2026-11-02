### Fixed

- **The test that pins the `-o` contract can no longer pass a command on a sibling's printer.**
  It finds a handler by the dispatch that calls it, not by a similar name.
