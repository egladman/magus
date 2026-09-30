### Fixed

- **`MAGUS_OFFLINE` refuses every `http\` request a script sends,** by name and before
  it leaves the process, as it already did for remote-spell fetches; `http\serve`, which
  only listens on localhost, is unaffected. `http` no longer claims its requests are
  audited: they are not recorded.
