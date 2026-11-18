### Removed

- **Breaking for SDK callers: `apiversion.Read`, `ReadHeader`, `ReadOption`,
  `WithBodyUnmarshal`, `Unmarshal` and `Parse`, with no replacement.** Nothing read
  through them; `APIVersion.Valid` checks a version's shape.
