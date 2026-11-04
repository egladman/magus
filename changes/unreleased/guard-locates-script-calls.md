### Fixed

- **The guard locates a script's own calls.** A line's script is judged only from the
  directories that call of it runs in, not from those of a same-named script at another
  path. A call reached through a wrapper still matches by name.
