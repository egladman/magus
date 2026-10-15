### Changed

- **`benchbeside` lints this repository.** An `// optimization:` comment, or a type named
  `...Cache` or `...Memo` with methods, needs a `func Benchmark` in its package's tests,
  or an `exempt` entry in `.golangci.yml` saying why.
- **`importceiling` is deleted.** The ceiling on `cmd/magus`'s imports is the `dep-fanout`
  `cmd/magus` threshold in `hack/policy/conformance.jsonl`, which counts every workspace
  import rather than `internal/` ones alone.
- **revive's `redundant-import-alias` is on,** and the import aliases that only repeated
  the package's own name are gone.
