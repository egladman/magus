### Fixed

- **`magus job fork` no longer leaves a `./magus` built at another commit.** An adopted
  checkout with a stale binary gets one built at its base, and a relative checkout in a
  `--stdin` record is made absolute.
