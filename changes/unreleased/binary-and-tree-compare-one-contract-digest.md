### Changed

- **Breaking: in a checkout of magus the guard compares contracts, not load errors.** A
  binary hashes the committed files it embeds, `api.lock` and the generated Buzz, against
  the tree's copies. A failed load counts against the binary only when they differ.
  `magus version -o json` reports the digest as `embedded.contract_sha256`.
- **Breaking: `api.lock` lists each compiled guard rule, store format and shipped spell.**
  A line is `guard-rule <name>`, `store <api_version>` or `spell <name>`.
- **Breaking: the one fix for a stale `./magus` is the relink.** `GOEXPERIMENT=jsonv2 go
  build -trimpath -o magus ./cmd/magus`, alone on its line, loads no magusfile. Any caller
  runs it in its own checkout, a leased worker included. The bootstrap over an existing
  `./magus` is refused and names it.
- **A binary that is not the checkout's `./magus` judges it when its contract matches.**
  The hook glue still falls to the next binary while one denies under `stale-binary`.
- **`magus job fork` judges an adopted checkout by its contract.** It refuses a checkout
  whose tree reads no rows of the job store's format. It rebuilds the checkout's `./magus`
  when that binary's contract differs from the tree's, not when its commit does.
- **`magus shell` judges any `magus.yaml` failure** instead of exiting with no verdict.
- **Breaking: MGS1021 is no longer inferred from a load error's wording.** A workspace
  that is not a checkout of magus gets MGS1021 only from its `required_version` floor.
