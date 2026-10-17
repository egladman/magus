### Changed

- **The installed agent skills teach the fix loop.** After a failed full run, magus-run reads the
  failure ref and reruns only the failing tests with `-- -run`, then runs the full target once.
  magus-multi-agent validates once per wave and runs `affected ci` once at the end.
