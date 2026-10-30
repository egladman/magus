### Changed

- **`go-build` passes `-trimpath`.** Checkouts of one source share build cache
  entries, as `go-test`, `go-vet` and `go-fuzz` already did.
