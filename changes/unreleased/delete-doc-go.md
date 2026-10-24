### Changed

- **A package keeps no package comment.** The code-comments rule now refuses a comment above
  the package clause in every Go file, `doc.go` included, and the `doc.go` files that held
  only a package comment are deleted.
