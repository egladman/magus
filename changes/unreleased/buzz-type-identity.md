### Fixed

- **Buzz type tests and qualified literals tell same-named types apart.** `is`, `as?`,
  a typed `catch` and a `<Node>` match arm match the object or enum in scope, so
  another module's private `Node` no longer passes `is Node`. A script's own type,
  function or variable no longer replaces one an import exports, and a later REPL
  line reaches the script's. Inside a module that declares its own `Node`,
  `ns\Node{...}` builds and type-checks as the `Node` that `ns` exports, and a default
  it leaves unset resolves in `ns`. All of it matches upstream Buzz.

### Changed

- **A literal of a type a host declares for type checking only fails to compile.**
  It names the type, instead of failing at run time in whatever branch reaches it.
