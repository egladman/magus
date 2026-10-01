### Fixed

- **Buzz type tests and qualified literals tell same-named types apart.** `is`, `as?`, a
  typed `catch` and a `<Node>` match arm match the object or enum in scope, so another
  module's private `Node` no longer passes `is Node`. A script's own declarations no
  longer replace an import's export, and `ns\Node{...}` builds the `Node` that `ns`
  exports. Matches upstream Buzz.
