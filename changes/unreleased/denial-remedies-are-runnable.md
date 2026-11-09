### Changed

- **Every remedy a deny serves is tested to run.** Each rule serving a next
  command has a test that judges it as every caller under this repository's
  policy. The command must parse, name a magus command that exists, and pass
  the same guard. A rule that starts serving a remedy fails until its test
  exists.
