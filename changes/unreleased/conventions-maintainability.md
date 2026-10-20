### Changed

- **`benchbeside` lints this repository.** An `// optimization:` comment, or a type named `...Cache` or `...Memo` with methods, needs a `func Benchmark` in its package's tests, or an `exempt` entry in `.golangci.yml` saying why.
