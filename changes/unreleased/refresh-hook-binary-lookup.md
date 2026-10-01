### Fixed

- **The refresh hook's `./magus` is looked for where git runs the hook.** The not-indexed
  diagnosis resolved a relative hook binary against the magus workspace root; it now uses
  the repository's top level, which differs when the workspace sits below it.
