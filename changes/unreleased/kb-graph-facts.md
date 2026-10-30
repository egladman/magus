### Fixed

- **The Go index covers `js && wasm` files.** A package the host excludes whole, such as
  `cmd/buzz-playground`, was in no listing, so `refs` could not see it or what it calls.
  The go spell now also indexes `GOOS=js GOARCH=wasm`, and a pack built with other
  configurations is rebuilt.
- **A symbol miss names only the gaps that could explain it.** A project in a language
  nothing indexes no longer turns a miss into `unknown` when the name appears nowhere in
  that project, and `explain` offers `graph build` only for a gap a build closes.

### Added

- **A package card says how to read past it.** `explain` on a Go package prints `go doc`,
  and where `hack/dev/show-dependency.buzz` exists, that script: docs ranked and the local
  source at the locked version, with `--fetch` for an absent one and a check against the
  lock. Nothing is built, run or indexed.
