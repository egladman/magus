### Fixed

- **The bootstrap runs while `./magus` cannot load the tree.** The raw-tool rule allows
  exactly the bootstrap command in the main session over a `./magus` older than the
  checkout, and the advice no longer says to move it aside first. A settle hook whose
  workspace fails to load for that reason names the bootstrap.
