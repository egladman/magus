### Added

- **`hack/remote/on-linux.buzz` runs a command on Linux from a Mac checkout.** `magus buzz
  hack/remote/on-linux.buzz -- magus affected ci` runs it unchanged in a local Podman container,
  with the checkout read-only and magus built from the tree; only `--env NAME` variables
  cross. It exits with the command's status, or 71 if the command never started. `--keep`,
  `--ls` and `--delete` manage leftover containers.
