### Fixed

- **The test that pins the `-o` contract can no longer pass a command on a sibling's printer.**
  It finds a command's handler by the dispatch that calls it, not by a name that resembles it,
  and a handler no longer reaches a printer through another command's handler.
