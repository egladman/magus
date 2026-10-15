### Added

- **`buzzengine`, the package that links the Buzz engine.** A Go program that embeds magus and evaluates magusfiles imports `_ "github.com/egladman/magus/buzzengine"`. Without it, `magus.Open` and `magus.Inspect` on a workspace with a magusfile return `*types.EngineNotLinkedError` ([MGS1104](https://eli.gladman.cc/magus/reference/codes/capability/MGS1104/)) naming the package, where they used to load the workspace with no magusfile targets.
