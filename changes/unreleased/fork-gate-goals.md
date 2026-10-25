### Fixed

- **`job fork` offers a ci gate only in a form the worker may run.** That is a test without
  default charms, never a build of magus. A gate no worker may run is the integrator's and
  no longer raises MGS3045, so a Go job needs no boilerplate deviations.
