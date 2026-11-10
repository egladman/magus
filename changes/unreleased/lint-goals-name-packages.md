### Changed

- **Breaking: go-vet vets the package of each file forwarded after `--`.** It
  passed the files to `go vet`, which type-checks them alone, so a file of a
  multi-file package failed. A non-Go file, or one `./...` leaves out such as
  testdata, is dropped with a note. A spell `Command` declares this with
  `pathScope` and `pathScopeFiles`.
