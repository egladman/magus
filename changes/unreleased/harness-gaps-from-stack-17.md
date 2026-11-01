### Fixed

- **A commit recording a conflict marker is refused.** The new `conflict-commit` rule reads
  what the commit would add through `git diff --check`. A fixture that must hold a marker
  sets git's `conflict-marker-size` attribute.
- **An integrating job may edit what its branches changed.** A write outside its write paths
  passes when a branch the row integrates changed that path since the checkpoint.
- **A `./magus` newer than the tree is no longer called older.** The load-contract check counts
  only what HEAD carries that the build lacks, and its refusal names the binary that judged.
- **A worker's own checkout has one route that always runs.** The refusal names
  `magus buzz hack/dev/bootstrap-checkout.buzz`, and the line still passes with
  `export BAGGAGE=...;` in front of it.
- **A Buzz job has a check it can run.** A script check is no longer refused at fork as
  `edit-ungraded`, an ignored output an untaken job regenerates no longer holds `buzz-test`,
  and MGS3040 offers a script check rather than `go-vet`.
- **A check nothing could grade names the newer binary first,** then `magus doctor`.
