### Fixed

- **Main-logic conformance counts a call into another Go module of the workspace.** A
  command function that calls into a module such as `libs/termkit` no longer reads as logic
  that calls no other package. The call is read from the file's references inside the
  function's body, since a call edge stops at the caller's own index.
