### Added

- **A throw below the top-level entry fails lint.** The `no-throw` rule in `hack/lint` parses
  every TypeScript and JavaScript file with the TypeScript compiler, `.tsx` and scripts outside
  any biome project included. A throw passes at a module's top level, in
  `unreachable(x: never)` and in the OpenCode plugin's guard hook. `hack/lint/no-throw.jsonl`
  records each file's existing throws; a file may not hold more, and a count above what its
  file holds fails too, so the counts only fall.
