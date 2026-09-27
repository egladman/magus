### Fixed

- **The bootstrap link accepts `-trimpath`.** `go build -o magus ./cmd/magus`
  is the one raw command a fresh worktree runs before it has a binary; adding
  `-trimpath` used to disqualify it as "not a bootstrap" since the rule
  treated any flag as unrecognized. It now stays recognized, and the printed
  bootstrap and rebuild hints show `-trimpath` themselves, so linking a
  binary no longer bakes the worktree's absolute path into its build cache
  keys.
