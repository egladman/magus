# orphanator

A Go static analyzer that reports a test file whose name **narrows** the name of
a source file beside it.

```text
resolver.go
resolver_test.go
resolver_edge_cases_test.go   <- reported: these tests belong in resolver_test.go
```

It ships as a plain `analysis.Analyzer` with no linter-runner dependency, plus a
golangci-lint module plugin in `plugin/`.

## What it does not report

The obvious rule - "every `X_test.go` must have an `X.go`" - is not the rule this
implements, because it is not what the Go community does. Measured against the
standard library of Go 1.26, excluding `src/cmd`:

| Rule | Files reported | Share of the 1348 test files |
| --- | --- | --- |
| Any test file with no source file of the same name | 660 | 49.0% |
| ...exempting the conventional names below | 459 | 34.1% |
| ...reporting only a name that narrows an existing source name | 51 | 3.8% |
| ...also exempting build-tag suffixes | **16** | **1.2%** |

A test file named for a cross-cutting concern that no single source file owns -
`concurrency_test.go`, `sizeof_test.go`, `issues_test.go` - is ordinary Go, and
a third of the standard library is written that way. Reporting it is a house
rule. It is available as `unpaired`, off by default.

Narrowing is the specific failure worth gating: the tests had a home and a new
file was created instead.

## Where the conventions come from

No published Go style guide states a test-file naming rule. The evidence is:

- **The toolchain** recognizes `*_test.go` as a test file, ignores names starting
  with `_` or `.`, and reads a trailing `_GOOS` / `_GOARCH` as a build
  constraint. It says nothing about pairing.
- **Go by Example** says the test for `intutils.go` "would then be named"
  `intutils_test.go` - a convention stated for the happy path, with no position
  on exceptions.
- **Google's Go Style Guide** covers test *package* naming (`linkedlist_test` for
  black-box tests) and is silent on test file naming and organization.
- **The standard library** is therefore the only real corpus, and the exemptions
  below are taken from it rather than invented. Counts are occurrences in
  `$GOROOT/src`.

Always exempt, by convention:

| Pattern | Uses in stdlib | Why it cannot pair |
| --- | --- | --- |
| `example_test.go`, `*_example_test.go` | 105 | godoc renders examples from it |
| `export_test.go`, `*_export_test.go` | 43 + 9 | the hatch exporting internals to an external test package |
| `fuzz_test.go`, `*_fuzz_test.go` | 15 | seed corpus and fuzz targets kept separate |
| `bench_test.go`, `*_bench_test.go`, `benchmark_test.go`, `*_benchmark_test.go` | 30 | benchmarks kept out of the unit test file |
| `main_test.go` | 6 | the `TestMain` entry point |
| `all_test.go` | 5 | package-wide suite |
| `internal_test.go`, `*_internal_test.go`, `*_pkg_test.go` | 6 | white-box companion to an external test package |

Also exempt: any trailing GOOS, GOARCH, or conventional build-tag segment
(`unix`, `posix`, `purego`, `cgo`, `stub`, ...), so `rawconn_unix_test.go`
continues to pair with `rawconn.go`. Without this the standard library reports
51 files instead of 16, and every one of the difference is a platform variant.

## Configuration

```yaml
version: "2"

linters:
  enable:
    - orphanator
  settings:
    custom:
      orphanator:
        type: module
        description: reports a test file whose name narrows a source file's name
        original-url: github.com/egladman/magus/libs/orphanator
        settings:
          # Additional exempt filename globs, filepath.Match syntax against the
          # base name. A malformed glob fails at config load, naming the pattern.
          allow:
            - "*_conformance_test.go"
          # Extend the rule to every test file with no source file of the same
          # name. See the table above for what this costs.
          unpaired: false
```

## Building the binary

golangci-lint cannot load a plugin at runtime; the binary is built with the
plugin compiled in. `.custom-gcl.yml` at the repo root declares it:

```bash
golangci-lint custom
```

That writes `./custom-gcl`, which reads `.golangci.yml` exactly as the stock
binary does. Note the tradeoff: once `orphanator` appears in `.golangci.yml`,
the stock binary can no longer read that config, so adopting this means every
lint entry point moves to the custom binary.

## Standalone use

The analyzer has no golangci-lint dependency, so it also runs under `singlechecker`
or as a `go vet` tool:

```go
singlechecker.Main(orphanator.Analyzer)
```

`Analyzer` is the default configuration: narrowing only, no extra exemptions. It
exposes no flags, so a standalone binary that needs `allow` or `unpaired` has to
build its own analyzer with `orphanator.New(orphanator.Options{...})`, which
returns an error on a malformed glob.

## Not hermetic

Whether `resolver.go` exists is a directory read, not something the analysis pass
can answer - an external test package is loaded with none of the package's source
files in it. A driver that caches analyzer results against declared package inputs,
as `go vet`'s unitchecker does, can therefore replay a stale verdict after a
sibling file is added or removed. Under golangci-lint this does not arise.
