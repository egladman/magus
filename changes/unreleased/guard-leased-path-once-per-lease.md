### Changed

- **The owned-path lease advisory speaks once per session per lease (`leased-path`).** It
  repeated on every write into a running lease's paths, 52% of all advisories served in one
  audit.
