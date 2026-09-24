### Changed

- **Breaking: an unknown key in `magus.yaml` fails the load.** MGS1040 reports each as
  `file:line` with the nearest known key; a second YAML document in a file is rejected.
