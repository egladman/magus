### Added

- **Every harness binds each hook point, or says why it cannot.** A hook a harness spell
  lists names the points it binds (`points` on `HookCommand`: shell, edit, stop,
  subagent-stop, spawn, session-start, compact, mcp, attach). A new contract function,
  `mgs_listUnsupportedHooks`, returns the rest, each with a reason.
