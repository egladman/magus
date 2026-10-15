### Changed

- **The Go SDK guide and the magus-sdk skill describe the SDK as it stands.** They
  cover linking `buzzengine` (MGS1104 without it), `JudgeCommand` and `JudgeWrite`,
  the guard judgment `Run` makes before anything runs, `WithEnv`, `WithStderr` and
  `WithCommandLine` with what each reads when left out, the three swappable parts and
  `std`'s typed options. The install section states the verified tags instead of a
  `replace` workaround, and `ExpandCwd` is gone from both.
- **The doctrine records two refusals**, an agent loop in the Go SDK and a model
  router. Friction that carries meaning lists the rules the SDK was read against.
