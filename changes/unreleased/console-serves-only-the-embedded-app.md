### Security

- **Breaking: the server serves only the console `MAGUS_CONSOLE_DIR` names.** It no longer
  falls back to the workspace's `console/gen`, so a cloned repository cannot ship the page
  that reads the token from its URL. Passing over a `console/gen`, or a `MAGUS_CONSOLE_DIR`
  with no `index.html`, is logged. To serve your build, set `MAGUS_CONSOLE_DIR` and restart.
