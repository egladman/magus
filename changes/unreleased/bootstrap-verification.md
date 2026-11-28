### Changed

- **Breaking: this repository's checkout scripts take Unix-style flags.**
  `hack/dev/bootstrap-checkout.buzz` builds in the current checkout, or in
  `-C <dir>`. It keeps a `./magus` already at HEAD unless `--force`. `--here`
  and `--checkout <dir>` are gone.
- **Breaking: readying a checkout to verify is its own script.**
  `hack/dev/bootstrap-verification.buzz` replaces `--verify-ready`. It builds only
  when `./magus` is stale, then refreshes pools, skill installs, the graph, the
  symbol digest and proofread.
