### Fixed

- **The Go index covers `js && wasm` files.** A package the host excludes whole, such as
  `cmd/buzz-playground`, was in no listing, so `refs` could not see it or what it calls.
  The go spell now also indexes `GOOS=js GOARCH=wasm`, and a pack built with other
  configurations is rebuilt.
- **A symbol miss names only the gaps that could explain it.** A project in a language
  nothing indexes no longer turns a miss into `unknown` when the name appears nowhere in
  that project, and `explain` offers `graph build` only for a gap a build closes.
- **`explain file:<path>` answers for that path or not at all.** A `file:` or `dir:` id with
  no node used to answer with the closest node of any kind and exit 0. It now reports the
  miss. Each project's manifests and lock files, `go.mod` and `go.sum` among them, are file
  nodes.
