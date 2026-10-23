### Added

- **The `splits` relation and the `file-split` conformance metric.** A file whose name stem extends another's at an underscore in the same directory (`R_cache.go`, `R_linux.go`, `R_test.go` beside `R.go`) now `splits` that root in the knowledge graph, so `magus explain file:R.go` lists what it was `split into`. Each edge carries the `suffix`, a `class` (`concept`, `platform`, `generated` or `test`) and `coupled`, whether the two share a symbol. `file-split` measures a concept sibling by the lines it and its root would make as one file, and is a floor: a sibling at or under the value is a violation, with `home` naming the root and `unshared` set when the two share nothing. It ships no default; a workspace configures it under `knowledge.conformance.checks`.

### Changed

- **The knowledge schema version is 24.** A store built before the `splits` relation rebuilds in full.
