### Fixed

- **The Go index covers `js && wasm` files.** A package the host excludes whole, such as
  `cmd/buzz-playground`, was in no listing, so `refs` could not see it or what it calls.
  The go spell now also indexes `GOOS=js GOARCH=wasm`, and a pack built with other
  configurations is rebuilt.
