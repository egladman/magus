### Fixed

- **`magus job apply` keeps the soft paths and integrator waivers the fork derived.** A
  record that leaves out `soft_paths` no longer hardens the test files the fork added as
  soft. The ones the job still writes stay soft. A record that names `soft_paths` for a path
  the job already held decides which paths stay soft. A record that leaves out a waiver
  for an output the integrator regenerates keeps that waiver.
- **A job's holder may remove a file it made after a narrowing drops it.** The guard lets
  `rm` and `mv` remove a file the holder's checkout created since the job's checkpoint,
  whether it is untracked or added by one of the holder's own commits, once that file is
  no longer in the write paths. A file the checkpoint already held, an ignored file, and a
  denied path still count as outside the lease.
