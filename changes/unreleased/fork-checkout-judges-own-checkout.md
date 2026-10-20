### Fixed

- **`magus job fork --stdin --checkout <dir>` makes the checkout it names.** `--stdin`
  dropped every flag beside it, so the fork judged the record against the forking
  checkout and refused it over a live job there. `--checkout` now fills a record naming
  no checkout, a record naming another is refused, and any other job flag with `--stdin`
  is refused.
