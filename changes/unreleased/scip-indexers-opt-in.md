### Added

- **External SCIP indexers return as an opt-in.** scip-typescript, scip-python and
  rust-analyzer run only for a spell `magus.yaml` lists under `spells.symbol_indexers`.
  Without it the language reports `no symbol index for <language>`, naming the setting;
  see [Symbol indexes](../../docs/guides/symbol-indexes.md).
