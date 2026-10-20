### Changed

- **The static-embed code is a library under `libs/embed`.** `internal/textembed`,
  `internal/rankeval` and the tokenizer from `internal/textrank` now live there, and the
  trainer moves from `hack/embed` to `libs/embed/train`, a module magus never imports.
  The retrieval eval runs as `magus run retrieval-eval-test .`; `hack/retrieval/harvest.buzz`
  is removed.
