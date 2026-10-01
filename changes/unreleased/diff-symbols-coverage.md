### Fixed

- **`magus diff` names the declarations a change touched in test files and build-tagged
  files.** A test function has no referents, so the filter dropping unreferenced locals
  dropped it; a touched declaration is now listed regardless. A Go file the symbol index
  leaves out, such as one under an unsatisfied build tag, has its declarations read from
  its own syntax.
