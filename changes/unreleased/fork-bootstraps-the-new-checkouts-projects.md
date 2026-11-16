### Fixed

- **`magus job fork --checkout` sets up a checkout whose revision lacks one of the
  forker's projects.** The install list skips a project whose directory the new
  checkout does not hold, so a project only the forker's tree has no longer fails the
  bootstrap.
