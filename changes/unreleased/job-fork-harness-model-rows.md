### Fixed

- **A job fork resolves a model named by a harness model row.** `magus job fork --model`
  and the Buzz `magus\job` calls map an id or alias such as `opus` to that row's tier.
  Before, only the CLI read the rows.
