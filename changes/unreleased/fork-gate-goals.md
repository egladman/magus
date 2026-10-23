### Fixed

- `job fork` offers a ci gate as a goal only in the form the guard lets the worker run: a test without default charms, never a build of magus. A gate no worker may run is the integrator's and no longer raises MGS3045, so a Go job needs no boilerplate waivers.
