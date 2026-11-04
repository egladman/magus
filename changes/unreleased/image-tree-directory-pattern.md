### Fixed

- **A Tree pattern that matches a directory covers every file below it.**
  `exclude = ["etc/ssl"]` now drops `etc/ssl/ca.pem`, which it used to keep.
