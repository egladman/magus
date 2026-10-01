### Fixed

- **Buzz type tests and qualified literals tell same-named types apart.** `is`, `as?`
  and a typed `catch` match the object or enum in scope, so another module's private
  `Node` no longer passes `is Node`. Inside a module that declares its own `Node`,
  `ns\Node{...}` builds and type-checks as the `Node` that `ns` exports. Both match
  upstream Buzz.
