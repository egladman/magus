### Added

- **`libs/train` splits and dedups the source files and builds the evaluation sets (stage S1).**
  `magus run split libs/train` links exact and near-duplicate files into one group and holds
  out about 10% of each language's bytes; `eval-sets`, `fim` and `g1` build the FIM and
  pass@k sets and recount the gate. Datasets stay in the cache; `gen/split.digest` pins them.
