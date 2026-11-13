### Changed

- **Breaking: the `unjudged-tool` rule is `unjudged-step`.** It also names a step of a
  kind no row declares. A `magus\guard.builtins` that names `unjudged-tool` fails to load
  (MGS1045): rename the key. No release shipped the old name.
  [ADR 0046](https://eli.gladman.cc/magus/decisions/0046-the-judge-takes-any-agent-step/).
