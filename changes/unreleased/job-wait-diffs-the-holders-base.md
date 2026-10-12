### Fixed

- **`magus job wait` diffs from the base the holder used.** The diff starts at the merge
  base of the holder's HEAD and its checkpoint, else its reported base, and names that
  base when it is not the checkpoint. A worker that switches onto its checkpoint after
  registering is recorded there, and the diverged-base advisory reads the live revision.
