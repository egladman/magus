### Fixed

- **`magus` means the checkout's build in a session and under magus.** A new
  `SessionStart` entry puts the session root first on PATH for the agent's Bash tool
  commands, and every child magus starts gets the running binary's directory first on
  PATH.
