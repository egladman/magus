### Fixed

- **The bootstrap link accepts `-trimpath`.** `go build -o magus ./cmd/magus`, the one
  raw command a fresh worktree runs before it has a binary, was refused as "not a
  bootstrap" when it carried `-trimpath`. It is recognized now, and the bootstrap and
  rebuild hints print `-trimpath`, so a linked binary keeps the worktree's path out of
  its build cache keys.
