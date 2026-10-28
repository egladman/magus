### Added

- **A module's declarations can import another module and name its types qualified.**
  `import "fs";` inside proc's declarations makes `fs\Path` in a field, a parameter or a
  return resolve to fs's own `Path`, never to a same-named type another module declares.
  A qualified name the declarations' own imports do not resolve fails the import with
  BZZ1009 or BZZ1002 naming the module, and a cycle between two declaration modules fails
  with BZZ2001.
