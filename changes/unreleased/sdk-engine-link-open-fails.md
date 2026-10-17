### Changed

- **Breaking for SDK callers: `magus.Open` and `magus.Inspect` fail without the engine.** A program that opened a workspace with a magusfile and did not link the engine now gets an error. The `magus` command links it and behaves as before.
