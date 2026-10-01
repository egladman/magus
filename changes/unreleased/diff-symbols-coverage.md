### Fixed

- **`magus diff` names the declarations a change touched in test files and build-tagged
  files.** A changed test function has no referents, so the filter that drops unreferenced
  locals dropped it too; a touched function, method, type, constant or variable is now
  listed whatever its reference count. A Go file the symbol index leaves out, such as one
  under a build tag the host does not satisfy, has its declarations read from its own
  syntax, with IDs under the module of its nearest `go.mod`, so a nested module's file is
  named under its own module.
- **Go code inside a string or comment no longer reads as a declaration.** The golang diff
  driver took `var i = 0;` in Buzz held by a Go raw string for a Go declaration, both where
  git places a change's regions and where magus places a hunk's. Lines that begin inside a Go string literal or
  block comment now stay with the declaration around them.
