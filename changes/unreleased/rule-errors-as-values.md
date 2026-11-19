### Added

- **Errors are values below the entry.** The `no-throw` rule in `hack/lint` also counts an
  exported TypeScript async function that returns no `Promise<Result<T>>`, and a Buzz function
  that declares `!>` and is no entry: a script `main`, an exported target or op, or an exported
  lint `check`. `hack/lint/no-throw.jsonl` holds today's counts, which only fall.
