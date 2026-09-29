### Added

- **`hack/on-linux.buzz` runs a command on Linux from a Mac checkout.**
  `magus buzz hack/on-linux.buzz -- magus affected ci` runs the command unchanged in a local
  Podman container: the checkout read-only at its own path, the Go image `mise.toml` pins,
  magus built from the tree, and only the variables named with `--env NAME` crossing. The
  exit status is the command's; 71 means the container never started it. `--keep`, `--ls`
  and `--delete` manage the containers it leaves. ADR 0003 records it as option B', revived
  while the `--platform` design is on hold.
