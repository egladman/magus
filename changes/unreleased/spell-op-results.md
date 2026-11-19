### Changed

- **Breaking: a spell op returns a Result instead of raising.** A provider op that can fail
  declares `> result\Result::<T>`, and magus reads its error as the failure, with the same
  words and code a raise carried; an op that cannot fail still returns `T`. An op that
  declares `!>` fails MGS1051 at load: return `result\fail` where it threw and
  `result\caught(err)` where a host call raised. A thrown string no longer prints the
  `buzz: uncaught error:` prefix. Every shipped op converts, and their `no-throw` rows are
  gone from `analyzers.yaml` ([ADR 0054](docs/decisions/0054-a-spell-op-returns-a-result.md)).
