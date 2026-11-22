### Fixed

- **A job store read reports every rejected field in store-owned objects on the same JSON line.**
  Each rejected field includes its path and array index so callers can correct
  the entry in one pass.
