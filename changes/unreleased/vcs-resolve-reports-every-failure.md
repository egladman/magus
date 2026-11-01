### Fixed

- **`magus vcs resolve` reports every failing regeneration in one pass.** It runs each rebuild
  target, lists each failing command and stages nothing. A `.gitattributes` pattern with a space
  is now quoted, and `magus init` says when a dirty build replaces a hook section a clean build
  wrote.
