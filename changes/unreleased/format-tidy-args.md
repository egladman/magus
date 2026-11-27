### Fixed

- **`go-mod-tidy` and `dprint` drop a forwarded file.** Neither takes a file operand, so `magus run format . -- a.go` made them refuse. The spells now declare it and the runner leaves the file out with a notice, in every workspace.
