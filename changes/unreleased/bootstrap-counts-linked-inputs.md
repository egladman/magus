### Fixed

- **The worktree bootstrap places a binary unless a linked package changed.** It
  counted every non-test Go file, so a package `./cmd/magus` never imports
  (`internal/textembed`) or a separate module (`hack/embed`) refused a current binary.
  It now counts only the packages `go list -deps ./cmd/magus` names, the files they
  embed, and the root `go.mod`, `go.sum` and `go.work`.
