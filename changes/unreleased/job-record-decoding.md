### Fixed

- **A job store read reports every unknown field on the same JSON line.**
  Each rejected field includes its path and array index so callers can correct
  the entry in one pass.
