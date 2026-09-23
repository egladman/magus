### Removed

- **Breaking: the `exclusive` target and project option, with no replacement.** A
  magusfile that sets it fails with MGS1038; delete the key. `slots` and `memory_mb` are
  the concurrency dials. The run-isolation gate goes with it. See docs/decisions/0001.
