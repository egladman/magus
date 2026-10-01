# testlayout

Two Go analyzers over the layout of test files, as plain `analysis.Analyzer`s with
no linter-runner dependency, plus a golangci-lint module plugin in `plugin/` that
registers each under its own name.

| Linter       | Reports                                                                      |
| ------------ | ---------------------------------------------------------------------------- |
| `testpair`   | every `X_test.go` with no `X.go` beside it                                   |
| `testlayout` | a test file in an external test package; optionally `_unix` names and `main` |

## testpair: every test file has a pair

```text
resolver.go
resolver_test.go
resolver_edge_cases_test.go   <- narrows resolver.go; these tests belong in resolver_test.go
concurrency_test.go           <- no source file of the same name
```

A test file pairs with:

- a source file of the same stem: `resolver_test.go` and `resolver.go`;
- that stem less the build suffixes the toolchain reads, every GOOS and GOARCH plus
  `unix`: `rawconn_linux_test.go` and `rawconn.go`;
- a platform family: `tree_test.go` and `tree_linux.go` with no `tree.go`.

Nothing else pairs, and nothing configures it. There is no conventional-name list,
so `example_test.go`, `main_test.go`, `export_test.go`, `fuzz_test.go` and
`bench_test.go` pair with `example.go`, `main.go` and the rest or are reported.
There is no allow list and no marker comment. The plugin refuses a settings block
at config load, since every option it could take would be an exemption.

The standard library would fail this on about half its test files (660 of 1348 in
Go 1.26, excluding `src/cmd`): a suite named after a concern, such as
`concurrency_test.go`, is ordinary Go. This rule is a house rule. A suite with no
single source file to cover gets one: the code it owns (its harness, its fixture
types, the corpus it walks) moves into a same-stem production file, and the suite
tests that file.

When some prefix of the stem names a source file, the message names the file the
tests belong to. A benchmark file gets its own message, since it always measures
some file.

### Build-tagged files

A test file a build constraint excludes on this platform, `store_linux_test.go` on
darwin or one behind `//go:build integration`, is in no pass's syntax. testpair
checks it anyway, from the pass's ignored files, parsing the package clause to
report at it. Source names come off disk, so a tagged source file still pairs.

### Why its own linter name

golangci-lint reports every analyzer a plugin builds under the plugin's name. A
tree that keeps an external test package to dodge an import cycle writes
`//nolint:testlayout` on its package clause, and if pairing reported as
`testlayout` too, that one directive would silence it. Under its own name it
cannot. This repository's `hack/lint/testlayout-pairing-has-no-exemptions.buzz`
refuses the remaining exits golangci-lint gives every linter: a nolint naming
`testpair` or `all`, a bare `//nolint`, an exclusion rule, a path exclusion, and a
module run whose `--enable-only` leaves it out.

## testlayout

A test belongs in the package it tests, so a file declaring `package foo_test` is
reported. The external package is for the case where an in-package test would
close an import cycle, and that case is worth a `//nolint:testlayout` naming the
cycle rather than a silent convention.

Two options, both off by default:

- `report-unix-suffix`: any Go file named `X_unix.go` or `X_unix_test.go` is
  reported. The toolchain reads `unix` from a `//go:build` line but never from a
  file name, so the name only suggests what the tag decides. Name the platforms
  instead: `X_linux.go`, `X_darwin.go`, `X_other.go`, and `X.go` for the shared
  part.
- `report-main-tests`: any `_test.go` declaring `package main` or `package
  main_test` is reported. A test that only compiles inside the binary it drives
  usually means the code it drives never left `package main` either.

magus leaves `report-main-tests` off. Its own `cmd/magus`, `cmd/magus-utils`, the
docs generators, gopherbuzz's CLI, and termcast/termshots/swegrade all test from
inside `package main` today, and this repo moves that logic into domain packages
over time rather than in one lift (see `plans/test-layers-lint-2026-09-26.md`).

## Build suffixes

Every GOOS and GOARCH from `go tool dist list` counts, plus `unix`. An earlier
version also carried `generic`, `other`, `stub`, `posix`, and `asm` because they
read like build tags. None of them is one, and each let `resolver_generic_test.go`
beside `resolver.go` pair silently. A suffix gets added only when the toolchain
acts on it.

## Configuration

```yaml
version: "2"

linters:
  enable:
    - testlayout
    - testpair
  settings:
    custom:
      testlayout:
        type: module
        description: reports a test file outside its package, or named for unix
        original-url: github.com/egladman/magus/libs/testlayout
        settings:
          report-unix-suffix: false
          report-main-tests: false
      # testpair takes no settings block.
      testpair:
        type: module
        description: reports a test file with no source file of the same name
        original-url: github.com/egladman/magus/libs/testlayout
```

## Building the binary

golangci-lint compiles plugins in rather than loading them at runtime, so you
build a binary carrying this one. `.custom-gcl.yml` in this directory declares it,
and `golangci-lint custom` reads that file from its working directory:

```bash
cd libs/testlayout && golangci-lint custom
```

`destination: ../../.magus` writes `.magus/custom-gcl`, which reads
`.golangci.yml` exactly as the stock binary does. `magus run lint` does both steps.

Once `testlayout` appears in `.golangci.yml` the stock binary can no longer read
it, so every lint entry point has to move to `.magus/custom-gcl`.

One binary carries every in-repo plugin, so a second linter is another entry in
`plugins:` rather than a second config file.

## Standalone use

The analyzers have no golangci-lint dependency, so `singlechecker`, `multichecker`
and `go vet` tools work too:

```go
multichecker.Main(testlayout.Pairing, testlayout.Analyzer)
```

`Analyzer` runs with every option off; build another with
`testlayout.New(testlayout.Options{...})`. `Pairing` has no options.

## Not hermetic

Whether `resolver.go` exists is a directory read, not something the pass can
answer: an external test package loads with none of its package's source files.
A driver that caches analyzer results against declared package inputs, as `go
vet`'s unitchecker does, can replay a stale verdict after you add or remove a
sibling file. golangci-lint does not hit this.
