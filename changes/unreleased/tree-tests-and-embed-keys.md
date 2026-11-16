### Changed

- **`lint` needs a new `conventions-test` target.** The eight tests that read the
  whole tree as data (environment sniffing, diagnostic raise sites, env var
  registration, JSON Lines stores, comment symbols, the `encoding/json` import,
  reveal caps) run there. A sized gate narrows `test` to the packages a change
  reaches and so dropped them; `lint` is never narrowed.

### Fixed

- **`libs/embed` and `retrieval-eval-test` key on the Go files they compile.**
  Declaring `testdata/*` replaced the project's sources in the key, so `lint`,
  `build` and `test` replayed a pass after a Go edit.
