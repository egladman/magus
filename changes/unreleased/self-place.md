### Added

- **`magus self place <binary>` replaces the running binary.** It renames a copy into
  place so the target gets a new inode; overwriting a binary in place gets every later
  exec killed on macOS. The replacement must report a clean commit stamp newer than the
  running build, or the verb refuses.
