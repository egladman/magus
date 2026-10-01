### Changed

- **One tool renders the terminal SVGs.** `cmd/magus-termshots` is now the
  `shots` subcommand of `cmd/magus-termcast`, and `termshots-generate` runs
  `magus-termcast shots`. Its outputs are unchanged. A word other than `shots`
  in the subcommand position is an error instead of being ignored.
