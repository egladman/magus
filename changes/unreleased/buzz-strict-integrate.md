### Changed

- **Breaking for SDK callers: `buzz.Parse` and the default session refuse Buzz written to the old rules.** Every argument after the first must be labeled, top-level statements must sit in `fun main()`, and generic type arguments must be named. Magus's runtime uses the embedded parse, which still accepts the old form.
