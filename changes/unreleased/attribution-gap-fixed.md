### Fixed

- **The git hooks spell's `remove` deletes a shim whose script is gone.** It
  finds one from any directory, so a shim left by a renamed hooks directory no
  longer skips its hook in silence.
