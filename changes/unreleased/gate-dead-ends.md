### Changed

- **Four agent dead ends in the job workflow are closed.** A `job fork --checkout` holder runs
  `affected ci` in its own checkout under any spawn title. `spawn-warm-worker` skips a warm
  worker of another role. A desktop `start_session` title counts as the spawn description.
  `job exit` without `--stdin` says no result was filed and how to file one.
