### Fixed

- **A sandboxed `go` can write the checksum database's state.** The go spell grants
  `$GOPATH/pkg/sumdb` (or `~/go/pkg/sumdb`), where go records the tree head it
  verifies a module against. Without it, `golangci-lint custom`'s `go mod tidy`
  failed under landlock whenever `GOMODCACHE` pointed elsewhere, as in the merge
  queue's candidates.
