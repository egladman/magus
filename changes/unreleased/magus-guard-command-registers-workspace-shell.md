### Added

- **`magus\guard.command` registers a workspace shell-command rule.** One Buzz function
  sees every agent shell command the built-ins pass, as parsed programs, and may deny or
  advise, never lift a built-in deny. It fails open and is held to the committed copy as
  `magus\guard.spawn` is. Both rules now return `GuardVerdict` (was `SpawnVerdict`) and
  share `once`/`count`.
