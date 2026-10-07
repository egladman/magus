### Changed

- **This repo's tests stop at the first failed `NoError`.** testifylint's
  `require-error` for `NoError` and `go-require` are enabled after sweeping the
  tree, so a test no longer carries on to read the nil result of a failed call and
  panic over the error that explains it.
