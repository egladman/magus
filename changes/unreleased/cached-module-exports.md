### Fixed

- **Cached Buzz imports keep their own exported values.** A module imported through
  a sibling alias no longer picks up another module's function with the same name.
  Basename, full namespace and relative namespace access all read the cached module.
