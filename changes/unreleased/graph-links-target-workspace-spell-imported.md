### Fixed

- **The graph links a target to a workspace spell imported without an alias.**
  `import "spells/acme";`, the form BZZ1008 requires, produced no target-to-op edges, so
  `magus path` and `magus explain` missed every op it runs.
