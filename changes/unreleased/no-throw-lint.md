### Added

- **A throw below the top-level entry fails lint.** The `no-throw` rule in `hack/lint` parses
  every TypeScript and JavaScript file, `.tsx` and scripts outside any biome project
  included. A throw passes at a module's top level, in
  `unreachable(x: never)` and in the OpenCode plugin's guard hook. `hack/lint/no-throw.jsonl`
  caps each file at its current count of throws, so the counts only fall.
