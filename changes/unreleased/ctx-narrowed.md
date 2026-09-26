### Added

- **`ctx.narrowed()` tells a target body a sized gate narrowed its tests.** A check
  over the whole suite, such as a coverage floor, can stand down when only the packages
  a change reaches ran.
