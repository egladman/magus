### Changed

- **`magus buzz --check` resolves `magus/spell/*` imports.** Each spell this build ships
  binds as a magusfile load binds it, with no workspace loaded, and a file importing one
  is read by the embedded rules. A misspelled import still fails BZZ2001, with a hint
  naming the spells that resolve. A `project/<path>` import still fails.
