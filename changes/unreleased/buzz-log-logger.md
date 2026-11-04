### Added

- **`log`'s `Logger` protocol is the injectable face of the module.** Same
  methods as the free functions. Flat-import the module (`import "log" as _`)
  then declare `object<Logger>` so a caller takes `logger: Logger` and receives
  Std or Quiet without rewriting call sites. Message text keeps `{expr}`
  interpolation; structured fields go in `attrs`.
