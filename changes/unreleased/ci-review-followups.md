### Changed

- **Docs generation caches independent content families.** API reference, proofread
  rules, and benchmark pages regenerate separately from the coupled runtime
  catalogs and worked examples. Each family owns its outputs and cleanup; the
  outer drift gate still checks all generated Markdown.

### Fixed

- **Local CI validates figures and raw CI logs remain readable.** Local docs CI
  checks figures after the site build; GitHub shards retain their separate required
  figures job. One-shot log reads advise using target artifacts without blocking
  jobs that lack them. Keep benchmark helpers with the search tests.
