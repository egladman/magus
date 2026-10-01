### Added

- **`vcs\regions` gives a script the declarations a change lands in.** One record per
  declaration each hunk touches, with its side, lines and the diff driver that named it: the
  footprint `magus job wait` prints, from the same implementation.
- **`hack/dev/split-into-branches.buzz --by symbols` splits a change by declaration.** Each
  layer lands after every changed declaration it references; a rename or signature change
  rides with its callers, a test with its code, generated output with its source. Layers pack
  up to `--budget` changed lines (400 by default) and each carries the command that proves it
  builds and passes its affected tests. A function needed a layer early is pulled down when it
  fits, or stubbed from its spell's stub body with its header kept byte for byte; a symbol
  neither can supply marks the layer `needs-stub`. `-o json` from either mode is the branch
  stack merge-job-branches reads.

### Changed

- **A `magus buzz` script builds the knowledge graph once.** Graph reads such as
  `magus\refs` share one build per script run, as a spell's already did, instead of
  rebuilding the symbol graph on every call.
