### Fixed

- **`explain file:<path>` answers for that path or not at all.** A `file:` or `dir:` id with
  no node used to answer with the closest node of any kind and exit 0. It now reports the
  miss. Each project's manifests and lock files, `go.mod` and `go.sum` among them, are file
  nodes.
