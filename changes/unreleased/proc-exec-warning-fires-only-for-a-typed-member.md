### Fixed

- **The `proc\exec` warning about the magus binary fires only when a typed member answers
  the invocation, and names it** (`call magus\run instead`). `magus job exec`,
  `magus queue ls` and other invocations with no member no longer warn.
