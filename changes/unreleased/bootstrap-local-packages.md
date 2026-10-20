### Fixed

- **Bootstrap recovery generates only checkout-local packages.** Require `.` or an
  explicit `./` package operand and reject paths escaping the build directory.
  Import paths such as `net/http` and unrestricted `...` no longer qualify for
  recovery. Remove the misleading `insidePackage` helper.
