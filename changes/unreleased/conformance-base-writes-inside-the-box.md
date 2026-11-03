### Fixed

- **Measuring a conformance base works under the merge-queue gate's sandbox.**
  `magus\change().mergeBase` exported the base and kept its measure under the user
  cache, which the gate denies. It now uses the workspace store, which every
  checkout shares and the gate grants, and `magus\change().conformance` reads the same place.
