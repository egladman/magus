### Added

- **The `splits` relation.** A file whose stem extends another's at an underscore in one directory (`R_cache.go`, `R_linux.go`, `R_test.go` beside `R.go`) now `splits` that root, so `magus explain file:R.go` lists what it was `split into`. Each edge carries `suffix`, `class` (`concept`, `platform`, `generated` or `test`) and `coupled`.
- **The `file-split` conformance metric.** It measures a concept sibling by the lines it and its root would make as one file, and is a floor: a sibling at or under the value is a violation, with `home` naming the root and `unshared` set when the two share no symbol.

### Changed

- **The knowledge schema version is 24.** A store built before the `splits` relation rebuilds in full.
