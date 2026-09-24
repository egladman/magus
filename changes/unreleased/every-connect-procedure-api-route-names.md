### Changed

- **Every Connect procedure and `/api/` route names its own need.** The daemon refuses to
  start on a missing or empty one, and an unloaded daemon holds the same needs. Graph
  reads need `console=read`. TokenService takes a `Grant` and lists each token's class;
  `TokenScope` is gone. A malformed share body is MGS9020, an impossible mint MGS9021.
