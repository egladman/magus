### Fixed

- **A failed fork bootstrap says what became of the checkout.** A checkout the fork made
  is removed and the error says so. An adopted checkout, or one an earlier fork of the job
  made, stays, and the error says that instead of claiming it was removed.
- **A re-fork into the job's own checkout starts at that checkout's head.** The commits
  above the old checkpoint are the job's own, so `magus job exec` no longer reads them as
  a divergence. The fork prints a note when it moves the checkpoint, and a fork naming no
  checkpoint starts at the head too.
- **`hack/dev/bootstrap-checkout.buzz` ends a running row whose work landed as a squash.**
  A head outside the target's history still ends the row when merging its work onto the
  target changes nothing.
