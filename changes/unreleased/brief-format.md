### Added

- **A spawn brief can be a typed `magus\Brief{...}` literal.** Set `agents.brief_format: buzz`
  in magus.yaml, or `magus job fork --brief-format buzz` for one job. The guard type-checks
  that brief at spawn and continue, refuses one with a wrong or missing field, and names
  the format and where it came from. `magus doctor` reports the workspace's format.
