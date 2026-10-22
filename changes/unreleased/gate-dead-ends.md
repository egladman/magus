### Changed

- **The holder of a `job fork --checkout` row runs `affected ci` in its own checkout,**
  whatever its spawn title; no integrator spawn is needed.
- **`spawn-warm-worker` skips a warm worker whose role differs** from the new spawn's, and
  `job apply` now reports a change to `follows` or `fresh_reason`.
- **A desktop `start_session` title counts as the spawn description.**
- **`job exit` without `--stdin` says no result was filed** and how to file one.
