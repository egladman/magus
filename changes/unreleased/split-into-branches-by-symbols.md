### Added

- **`hack/dev/split-into-branches.buzz --by symbols` splits a change by declaration.**
  Each layer lands after every changed declaration it references: a rename rides with its
  callers, a test with its code, generated output with its source. Layers pack up to
  `--budget` changed lines (default 400), each with the command that proves it builds and
  passes. `-o json` is the branch stack merge-job-branches reads.
