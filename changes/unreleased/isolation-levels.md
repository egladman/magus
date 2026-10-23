### Changed

- **A fork places the forker's ./magus whenever no build input differs from its commit.** A
  checkout at a base other than the binary's commit used to get no binary and wait on a
  bootstrap build. The fork now places it when no file `go list -deps ./cmd/magus` reaches,
  and no root module file, differs between the two commits, so a docs or console change no
  longer costs a build.
