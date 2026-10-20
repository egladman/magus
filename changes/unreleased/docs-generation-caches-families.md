### Changed

- **Docs generation caches independent content families.** API reference, proofread
  rules, and benchmark pages regenerate separately from the coupled runtime
  catalogs and worked examples. Each family owns its outputs and cleanup; the
  outer drift gate still checks all generated Markdown.
