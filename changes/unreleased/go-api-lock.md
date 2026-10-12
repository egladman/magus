### Added

- **The public Go API has a drift-gated lock.** `internal/cli/testdata/api-go.lock` lists
  every exported declaration of the root package, `types`, `libs/testkit` and
  `types/gen/mocks` with its full signature, so a changed signature shows in review.
  `magus run go-api-generate .` rewrites it.
