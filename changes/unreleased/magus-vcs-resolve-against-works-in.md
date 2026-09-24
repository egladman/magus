### Fixed

- **`magus vcs resolve --against` works in a linked worktree, and paths stage literally.**
  A conflicted merge there read as one that never started, and a file named `*.txt`
  staged every `.txt` file. A merge already underway is now refused.
