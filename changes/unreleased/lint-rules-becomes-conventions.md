### Changed

- **Breaking: the root `lint-rules` target is now `conventions`.** It runs
  `hack/lint/run.buzz`, the repository's own checks on its non-Go files, and is the root's
  counterpart of `docs:conventions`. Run `magus run conventions .`; `lint-rules` has no
  alias. Its footprint rule is now `conventions-keys-on-what-the-rules-read`.
