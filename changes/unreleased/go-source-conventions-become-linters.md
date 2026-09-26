### Changed

- **Go-source conventions run in `magus run lint`.** Ten tree-walking tests in
  `conventions_test.go` became golangci-lint analyzers in `libs/conventions`
  (`hostagnostic`, `hostvocab`, `ruletext`, `asciistrings`, `importceiling`,
  `stutter`, `nameoutput`, `testisolation`) plus a `depguard` rule for `types`.
  They report at the offending line and take `//nolint:<name>`.
