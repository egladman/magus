### Fixed

- **`magus vcs resolve` reports every failing regeneration in one pass.** It ran each rebuild
  target even after one failed, listed each failing command, and still staged nothing. A stage
  that fails before regeneration names the stages that did not run.
- **A `.gitattributes` pattern with a space is quoted.** `docs/reference/config *.md` was
  written bare, which git rejects.
- **`magus init` says when a dirty build replaces a hook section a clean build wrote.** An
  unchanged section is not rewritten.
