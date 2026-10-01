### Fixed

- **The magus-buzz-lang skill's examples run.** They import `encoding/json`, list a
  directory with `fs\listDir` and raise from an exported `main`, and the module list
  names the `encoding/` modules by their import paths. `flags\parse` says a flag is
  declared with its dashes.
