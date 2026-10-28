### Added

- **External SCIP indexers return as an opt-in.** The typescript, python and rust spells
  declare scip-typescript, scip-python and rust-analyzer again, and each runs only when
  `magus.yaml` lists its spell under `spells.symbol_indexers`
  (`symbol_indexers: [typescript]`). Without the entry the language keeps reporting
  `no symbol index for <language>`, and that message now names the setting. External
  indexers are slower than magus's in-process Go indexer; see
  [Symbol indexes](../../docs/guides/symbol-indexes.md).
