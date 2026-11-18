### Removed

- **Breaking for SDK callers: `apiversion.Read`, `ReadHeader`, `ReadOption`,
  `WithBodyUnmarshal`, `Unmarshal` and `Parse`, with no replacement.** Nothing read
  through them; `APIVersion.Valid` checks a version's shape.
- **Breaking for SDK callers: `job.ReadRecord`, `Store.Reader` and `Store.AppendAttempts`.**
  A `*job.Store` is a `job.Reader` itself; attempts are appended by the store's own
  writes.
- **Breaking for SDK callers: `codec.StrictDecoder` and `NewStrictDecoder`.** Use
  `codec.UnmarshalStrict`.
