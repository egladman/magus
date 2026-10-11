### Changed

- **Breaking for SDK callers: `Magus.PayloadByRef` takes a `context.Context` first.** A
  miss reads the primary checkout's store next; the context now ends that search where
  it used to run under `context.Background()`. Pass the context of the call that needs
  the payload.
