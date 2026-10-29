### Changed

- **The Go symbol indexer is chosen by format.** A spell declares
  `format = SymbolFormat.goPackages` with the package patterns as the command's args, in
  place of a `go list <patterns>` command.
