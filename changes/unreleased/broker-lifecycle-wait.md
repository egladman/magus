### Removed

- **Breaking: `--wait` on `run` and `affected`, with no replacement.** It waited on a run
  handed to the server, and `--detach` no longer hands a run to anything; to wait for a
  run, leave off `--detach`.
