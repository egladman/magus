### Changed

- **Buzz module declarations can import another module and name its types qualified.**
  With `import "fs";` in proc's declarations, `fs\Path` resolves to fs's own `Path`. An
  unresolved qualified name fails the import, naming the module. `object<mod\Proto>`
  parses, and an enum passed where a builtin method takes a str fails with BZZ1005.
