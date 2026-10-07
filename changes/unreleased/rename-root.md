### Fixed

- **`refs --rename` under `--root` no longer panics when run from another checkout.** Its
  guard grading inspects, and loads its rules from, the workspace the command loaded
  instead of the one the shell sits in.
