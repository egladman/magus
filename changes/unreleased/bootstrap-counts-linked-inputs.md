### Fixed

- **The worktree bootstrap places a binary unless a linked package changed.** It
  counted every non-test Go file in the tree, so a package nothing in `./cmd/magus`
  imports (`internal/textembed`) or a separate module (`hack/embed`) refused a binary
  that was provably current. It now counts only Go files in the packages
  `go list -deps ./cmd/magus` names, the files they embed, and the root `go.mod`,
  `go.sum` and `go.work`.
