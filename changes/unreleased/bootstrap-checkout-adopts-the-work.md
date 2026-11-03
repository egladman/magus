### Added

- **One line resumes another agent's work in the wrong checkout.** `./magus buzz
  hack/dev/bootstrap-checkout.buzz -- --here --onto <ref>` fast-forwards a clean checkout
  onto `<ref>`, builds `./magus` when the present one cannot load the tree, installs
  dependencies and every harness, ends running rows whose head is already in `<ref>`, and
  prunes the rows nobody collected. A dirty tree or a move that is not a fast-forward
  refuses with the reason, and nothing is reset. It prints a line per thing it did.
