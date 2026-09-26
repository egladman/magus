### Changed

- **MGS1040 reads as a list.** Each unknown `magus.yaml` key prints once, by its dotted
  path, with every line it appears on and any suggestion. The version gap is stated once, followed by the fixes
  as commands, including the `go build` bootstrap for a dev build in a magus checkout.
