### Changed

- **`clikitmain` runs in `magus run lint`.** A main package that defines or parses a flag
  through package `flag`, or reads `os.Args`, fails lint. Each module's `analyzers.yaml`
  holds today's count per file under `clikitmain`, which only shrinks.
