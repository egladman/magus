### Added

- **`buzzengine`, the package that links the Buzz engine.** A Go program that embeds
  magus and evaluates magusfiles imports `_ "github.com/egladman/magus/buzzengine"`.
  Without it, `magus.Open` and `magus.Inspect` on a workspace with a magusfile return
  `*types.EngineNotLinkedError` ([MGS1104](https://eli.gladman.cc/magus/reference/codes/capability/MGS1104/)) naming
  the package, where they used to load the workspace with no magusfile targets.

### Changed

- **Breaking: `magus.Open` and `magus.Inspect` fail without the engine.** A program that
  opened a workspace with a magusfile and did not link the engine now gets an error. The
  `magus` command links it and behaves as before.
