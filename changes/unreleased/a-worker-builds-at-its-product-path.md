### Added

- **A relinking target declares a product path: `"product": {"path", "target"}`.** It sits
  beside `relinks` in `magus.project`'s targets. The path is where a leased worker builds
  the relinked binary, and the target is the sibling that writes it. The path keeps the
  relinked binary's file name, and a declaration without `relinks` is refused. magus's own
  `go-build` declares `bin/magus`, written by `go-build-product`.
- **A leased worker builds magus at its product path.** The guard lets it run the product
  target and write that path, and reads a line running it as a magus line. No hook runs
  the product, so the binary the row pins still judges the worker. Its relink of `./magus`
  stays refused, and the refusal names the product build.
