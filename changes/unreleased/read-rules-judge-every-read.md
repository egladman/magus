### Changed

- **The read rules judge every whole read, not just the easy ones.** `read-navigation`
  maps a Go file from a parse of the file itself, so a stale index still gets the map,
  each declaration served as `sed -n <first>,<last>p <file>` where refs cannot vouch
  for it. It judges every file a `cat` prints, maps Buzz files (fun, test, object,
  enum), and leaves SKILL.md, AGENTS.md and CLAUDE.md to be read whole. The Claude
  Code and Codex harnesses wire the command guard to the `Read` tool too, restated as
  the `cat` or `sed -n` line it stands for; re-merge what `magus describe harness`
  prints to pick it up.

### Added

- **`grep-reader` refuses grep as a reader.** A definition lookup with a context
  flag (`grep -A40 'func X' f.go`) is served `magus refs X --definition --source`,
  or the declaration's own lines when the index cannot vouch for the name.

### Fixed

- **`interpreter-rewrite` judges what a script writes, not what it mentions.** A
  script that carries tracked paths as data and writes its report to scratch or
  stdout runs; only a write's destination is checked against the tree.
