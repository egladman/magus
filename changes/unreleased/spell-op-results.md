### Changed

- **Breaking: a spell op returns a Result instead of raising.** An op that can fail
  declares `> result\Result::<T>`, and magus reads its error as the failure. An op
  declaring `!>` fails MGS1051 at load; return `result\fail` or `result\caught(err)`
  instead. A thrown string loses its `buzz: uncaught error:` prefix
  ([ADR 0054](docs/decisions/0054-a-spell-op-returns-a-result.md)).
