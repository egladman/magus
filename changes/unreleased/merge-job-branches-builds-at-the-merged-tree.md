### Fixed

- **`merge-job-branches --apply` regenerates with a `./magus` built at the merged tree.**
  It used to run the caller's binary there, so `symbol-digest-generate` stalled or failed
  against a revision that binary was not built at. It now builds one at the tree's HEAD
  with the `bootstrap-checkout` gate-ready recipe and regenerates with that. When any step
  fails the tree is kept, and the error names its path, the failing target, its output
  ref and one cause line, not the log's JSON.
