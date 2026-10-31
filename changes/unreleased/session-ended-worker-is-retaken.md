### Fixed

- **Continuing a worker whose session ended advises the retake.** The guard told the root
  to fork a new job for a row that ended `no_return` with its worker's session. It now
  names `job exec <id>`, which the resumed worker runs to take its row back. Other ended
  rows keep the fork advice.
