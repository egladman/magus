### Added

- **A `SymbolIndexer` can name other spell tools in `uses`.** Their versions key the
  symbol index, never a build or test (the go spell names `go`). A `uses` entry that is
  not a version-probed tool is a load error.
