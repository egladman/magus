### Fixed

- **A broken working tree no longer switches off the approved spawn rule.** The committed
  `magus\guard.spawn` rule runs on every spawn whatever the working tree holds; resolving
  it too slowly denies. A skipped rule says what applied. A workspace advise joins a
  built-in one, and the idle clock follows the agent's id.
