### Changed

- **A fork places the forker's ./magus whenever no build input differs from its commit.** A
  checkout at another base used to get no binary and wait on a bootstrap build. Now a docs
  or console change no longer costs one: only a file `go list -deps ./cmd/magus` reaches,
  or a root module file, keeps the binary out.
