### Fixed

- **`magus job apply` keeps the soft paths and integrator waivers the fork derived.** A
  record that leaves out `soft_paths` no longer hardens the test files the fork added as
  soft. The ones the job still writes stay soft. A record that names `soft_paths` for a path
  the job already held decides which paths stay soft. A record that leaves out a waiver
  for an output the integrator regenerates keeps that waiver.
