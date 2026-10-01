### Changed

- **The gha-run script is `hack/remote/on-actions.buzz`, a prefix.** `magus buzz
  hack/remote/on-actions.buzz -- magus affected ci` pushes HEAD, runs the command on a GitHub
  Actions runner, waits, prints its output, exits with its exit code, and deletes the run
  and the branch unless `--keep`. `--detach`, `--result <run>`, `--delete <run>` and `--ls`
  manage runs; `dispatch`, `result` and `forget` are gone.
