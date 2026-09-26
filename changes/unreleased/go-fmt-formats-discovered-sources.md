### Changed

- **The go spell's `go-fmt` formats the Go files source discovery finds.** It no longer
  walks dot directories, `gen/` or `vendor/`, so a module cache kept in `.magus` is
  neither listed nor rewritten.
