### Changed

- **Graph reads fold only the session files written since the last read.** The agent
  contacts the `@session` overlay counts are cached beside the session store, keyed by each
  invocation file's name, size, modification time and inode, so a read no longer decodes
  every file in the store. The store is also capped at its newest 10,000 invocation files,
  sparing the newest 500 loaded host sessions, and schema-1 files now age out like any other.
- **Tool-version probes for go, golangci-lint, buf and `pnpm exec tsc` are cached.** The
  key is the resolved binary's identity plus what picks its version (for go, the go and
  toolchain lines of go.work and go.mod, the GOENV file and GOTOOLCHAIN), not the project
  directory, so projects that see the same inputs share one answer and one fork. A failed
  probe is cached for ten minutes.
- **The scip op keys on its language's sources and its indexer's version.** A rewrite of
  installed skills or an install that adds `node_modules` no longer marks a symbol index out
  of date, and upgrading scip-go or scip-typescript now does. Each indexing spell declares
  its indexer as an observed tool, so its version keys only the scip op. A `SymbolIndexer`
  can name the spell's tools it also runs in `uses` (the go spell names `go`), and their
  versions key the index too, never a build or test; a `uses` entry that is not a
  version-probed tool is a load error.
- **The push rule reads only gate verdicts.** It used to decode every invocation in the
  session store on each push; it now reads the gate results through the same per-kind cache.
