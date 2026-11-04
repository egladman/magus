### Added

- **One line resumes another agent's work in the wrong checkout.** `./magus buzz
  hack/dev/bootstrap-checkout.buzz -- --here --onto <ref>` fast-forwards a clean checkout
  onto `<ref>`, builds `./magus` if needed, installs dependencies and harnesses, ends rows
  already in `<ref>`, and prunes uncollected rows. A dirty tree or non-fast-forward move
  refuses; nothing is reset.
