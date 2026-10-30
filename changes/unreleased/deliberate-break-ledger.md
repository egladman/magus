### Added

- **A deliberate break carries its own way to green.** A comparison gate reads
  `<project>/<gate>.deviations.jsonl`, and `proto` `ci` is the first. A finding with no
  line fails and prints the line for a person to add, a line with no finding fails, and
  a passing gate warns with the file and count. The `deviation-author` guard rule refuses
  an agent adding a line.

### Changed

- **`buf-breaking` captures and has no `gha` charm.** A caller reads its report from the
  record the op returns.
