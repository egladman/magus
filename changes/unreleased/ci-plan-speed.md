### Changed

- **The root `generate` no longer builds the full symbol index.** It leaves out
  `symbol-digest-generate`, whose `gen/symbol-index.json` is gitignored and has no drift to
  check. Its readers, `libs/embed/train:generate` and `docs:diagrams-generate`, still need
  it themselves. CI's plan job leaves `libs/embed/train` out of its generate pass, so it no
  longer spends 7-12 minutes on the index before any shard starts.
