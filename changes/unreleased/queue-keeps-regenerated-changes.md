### Changed

- **The queue keeps a change whose generated files its merge leaves alone.** It kicks
  back with `KICK_REGENERATION` only when the merge needs them regenerated (a conflict,
  or main changed them) by code the change touches; otherwise the drift gate checks them.
  A kicked-back change merges main in, regenerates, and is queued again.
