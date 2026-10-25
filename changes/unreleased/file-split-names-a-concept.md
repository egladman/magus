### Changed

- **A `file-split` sibling names one concept.** `R_X_Y.go` with no `R_X.go` is now a violation whatever its size, so a root past the floor no longer lets any name through; the item's `rename` names the file to write instead, `R_X.go`. Beside `R_X.go`, `R_X_Y.go` is judged as its sibling. Tests, platform builds and generated files are never findings.
