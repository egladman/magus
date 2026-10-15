### Added

- **`magus diff` reports a copied call sequence.** The `call-sequence` check, on by default as advice, names an added, renamed or re-signed function that calls the same workspace functions in order as another, past the configured run length. It skips one-statement wrappers, builders of mostly one type's methods and Go command verbs taking a `[]string`. Silent without a symbol index.
