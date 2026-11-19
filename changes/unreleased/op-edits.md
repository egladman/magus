### Changed

- **Breaking: an op that rewrites files in place passes when run directly.**
  `magus run go-fmt:rw`, `go-mod-tidy:update` and `typos:fix` failed MGS4007 after
  writing. A Command now lists the files it rewrites in `edits`; a composing target
  inherits nothing. The check variant still fails on drift. Every cache key moves once.
  [ADR 0053](https://eli.gladman.cc/magus/decisions/0053-an-op-declares-the-files-it-edits-in-place/).
