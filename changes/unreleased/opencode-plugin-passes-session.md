### Fixed

- **The OpenCode plugin passes its session to the guard.** Every judged call now carries
  OpenCode's session id as `--session`, so the push rule, which refuses a call that names
  no session, no longer refuses every push from OpenCode.
