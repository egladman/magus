### Added

- **The agent guard refuses a script rewriting a file that holds merge conflict markers.**
  The `conflict-rewrite` rule denies an inline python, perl, ruby, node, awk or Buzz program
  that writes such a file, or that carries a conflict marker and writes a file it does not
  name. A script that opens a file for writing and then raises leaves it empty. The rule
  denies by default, so it holds while a conflicted policy keeps the workspace's own rules
  from loading. `interpreter-rewrite` also reads a destination bound by a loop
  (`for path in [...]`).

### Changed

- **A guard notice names a conflicted policy file.** When the magusfile fails to load and
  the magusfile or a policy file it imports holds conflict markers, every notice that a
  workspace rule judged nothing opens by saying the guard rules are off, which file to
  resolve first, and the command that resolves its hunks when the workspace carries one.
