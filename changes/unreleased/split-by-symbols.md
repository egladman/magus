### Added

- **`vcs\regions` gives a script the declarations a change lands in.** One record per
  declaration each hunk touches, with its side, lines and the diff driver that named it: the
  footprint `magus job wait` prints, from the same implementation.
- **`hack/dev/split-into-branches.buzz --by symbols` splits a change by declaration.** Each
  layer lands after every changed declaration it references; a rename or signature change
  rides with its callers, a test with its code, generated output with its source. Layers pack
  up to `--budget` changed lines (400 by default) and each carries the command that proves it
  builds and passes its affected tests. A failed proof given back with `--failed` marks the
  layer `needs-stub`, naming the missing symbol and the layer introducing it; `--pull` moves
  that symbol down instead. `-o json` from either mode is the branch stack merge-job-branches
  reads.
