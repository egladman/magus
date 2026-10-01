### Fixed

- **Buzz type tests and qualified literals tell same-named types apart.** `is`, `as?`,
  a typed `catch` and a `<Node>` match arm match the object or enum in scope, so
  another module's private `Node` no longer passes `is Node`. A script's own `Node` no
  longer replaces a `Node` an import exports. Inside a module that declares its own
  `Node`, `ns\Node{...}` builds and type-checks as the `Node` that `ns` exports, and a
  default it leaves unset resolves in `ns`. All of it matches upstream Buzz.
