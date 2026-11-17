### Changed

- **`lint` needs a new `conventions-test` target.** The eight tests that read the
  whole tree as data (environment sniffing, diagnostic raise sites, env var
  registration, JSON Lines stores, comment symbols, the `encoding/json` import,
  reveal caps) run there. A sized gate narrows `test` to the packages a change
  reaches and so dropped them; `lint` is never narrowed.
