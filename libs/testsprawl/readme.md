# testsprawl

A Go analyzer that reports a test file whose name **narrows** the name of a source
file beside it.

```text
resolver.go
resolver_test.go
resolver_edge_cases_test.go   <- reported: these tests belong in resolver_test.go
```

It ships as a plain `analysis.Analyzer` with no linter-runner dependency, plus a
golangci-lint module plugin in `plugin/`.

## What it does not report

The obvious rule, "every `X_test.go` needs an `X.go`", is not what this
implements. Measured against the Go 1.26 standard library, excluding `src/cmd`:

| Rule                                                          | Reported | Share of 1348 test files |
| ------------------------------------------------------------- | -------- | ------------------------ |
| Any test file with no source file of the same name            | 660      | 49.0%                    |
| ...exempting the conventional names below                     | 459      | 34.1%                    |
| ...reporting only a name that narrows an existing source name | 51       | 3.8%                     |
| ...also exempting build-tag suffixes                          | **16**   | **1.2%**                 |

A third of the standard library names test files after a cross-cutting concern no
single source file owns: `concurrency_test.go`, `sizeof_test.go`. Reporting those
is a house rule, available as `unpaired` and off by default.

Narrowing is the case worth gating. The tests had a home and someone made a new
file instead.

## Where the conventions come from

No published Go style guide states a test-file naming rule. Go by Example says the
test for `intutils.go` "would then be named" `intutils_test.go`, with no position
on exceptions. Google's style guide covers test _package_ naming and skips file
naming. The toolchain reads `_test.go`, ignores names starting with `_` or `.`,
and treats a trailing `_GOOS`/`_GOARCH` as a build constraint. None of them
mention pairing.

That leaves the standard library as the only corpus, so the exemptions below come
from counting it rather than from taste. Counts are occurrences in `$GOROOT/src`.

| Pattern                                                   | Uses | Why it cannot pair                              |
| --------------------------------------------------------- | ---- | ----------------------------------------------- |
| `example_test.go`, `*_example_test.go`                    | 105  | godoc renders examples from it                  |
| `export_test.go`, `*_export_test.go`                      | 52   | exports internals to an external test package   |
| `fuzz_test.go`, `*_fuzz_test.go`                          | 15   | seed corpus kept separate                       |
| `bench_test.go`, `benchmark_test.go` and their `*_` forms | 15   | benchmarks kept out of the unit test file       |
| `main_test.go`                                            | 6    | the `TestMain` entry point                      |
| `all_test.go`                                             | 5    | package-wide suite                              |
| `internal_test.go`, `*_internal_test.go`, `*_pkg_test.go` | 6    | white-box companion to an external test package |

Trailing GOOS and GOARCH segments are also exempt, plus `unix`, so
`rawconn_unix_test.go` still pairs with `rawconn.go`. Without that the standard
library reports 51 files instead of 16, and every one of the difference is a
platform variant.

Nothing else earns a place on that list. An earlier version carried `generic`,
`other`, `stub`, `posix`, and `asm` because they read like build tags. None of
them is one, and each turned `resolver_generic_test.go` beside `resolver.go` into
a silently exempt file. Nobody reports a linter for staying quiet, so a suffix
gets added only when the toolchain acts on it.

## Configuration

```yaml
version: "2"

linters:
  enable:
    - testsprawl
  settings:
    custom:
      testsprawl:
        type: module
        description: reports a test file whose name narrows a source file's name
        original-url: github.com/egladman/magus/libs/testsprawl
        settings:
          # Extra exempt globs, filepath.Match against the base name. A malformed
          # glob fails at config load and names the pattern.
          allow:
            - "*_conformance_test.go"
          # Report every test file with no source file of the same name. See the
          # table above for what this costs.
          unpaired: false
```

## Building the binary

golangci-lint compiles plugins in rather than loading them at runtime, so you
build a binary carrying this one. `.custom-gcl.yml` in this directory declares it,
and `golangci-lint custom` reads that file from its working directory:

```bash
cd libs/testsprawl && golangci-lint custom
```

`destination: ../..` writes `./custom-gcl` at the workspace root, which reads
`.golangci.yml` exactly as the stock binary does. `magus run lint` does both steps.

Once `testsprawl` appears in `.golangci.yml` the stock binary can no longer read
it, so every lint entry point has to move to `./custom-gcl`.

One binary carries every in-repo plugin, so a second linter is another entry in
`plugins:` rather than a second config file.

## Standalone use

The analyzer has no golangci-lint dependency, so `singlechecker` and `go vet`
tools work too:

```go
singlechecker.Main(testsprawl.Analyzer)
```

`Analyzer` is the default configuration and exposes no flags. To set `allow` or
`unpaired` outside golangci-lint, build your own with
`testsprawl.New(testsprawl.Options{...})`, which errors on a malformed glob.

## Not hermetic

Whether `resolver.go` exists is a directory read, not something the pass can
answer: an external test package loads with none of its package's source files.
A driver that caches analyzer results against declared package inputs, as `go
vet`'s unitchecker does, can replay a stale verdict after you add or remove a
sibling file. golangci-lint does not hit this.
