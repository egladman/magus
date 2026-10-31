### Fixed

- **A magus under `magus job exec --timeout` runs under the wrapper's budget claim.** A
  `magus job fork` or `magus run` started by the command no longer fails MGS3009 against
  the slots its own wrapper holds. The broker protocol is now 4: stop a running broker
  of an older build with `magus broker stop`.
