# Contributing to magus

## Getting started

```sh
git clone https://github.com/egladman/tack
cd tack/magus
go build ./cmd/magus
go test -race ./...
```

LuaJIT is required for the full test suite on Linux and macOS:

```sh
# Linux
sudo apt-get install libluajit-5.1-dev pkg-config

# macOS
brew install luajit pkg-config
```

Windows uses the pure-Go gopherlua backend (`CGO_ENABLED=0`).

## Test conventions

This repo distinguishes unit tests from integration tests with four layered signals.
A test is an integration test if **any** of these are true:

- It shells out (`exec.Command`, invokes `go build`, `git`, …).
- It needs the network, a daemon, or a real binary on `$PATH` other than `go`.
- It spins up a container, server, or other long-lived process.
- It takes more than ~500ms in the steady state.

Otherwise it is a unit test. Unit tests should run the entire suite in a few seconds.

| Test type   | Filename                  | First line               | Package            | Function name           |
| ----------- | ------------------------- | ------------------------ | ------------------ | ----------------------- |
| Unit        | `foo_test.go`             | (none)                   | `package foo`      | `Test<Name>`            |
| Integration | `foo_integration_test.go` | `//go:build integration` | `package foo_test` | `TestIntegration<Name>` |

```sh
go test ./...                                    # unit only (fast)
go test -tags=integration -run=Integration ./... # integration only
go test -tags=integration ./...                  # both
```

## Linting

```sh
golangci-lint run
```

The project's `.golangci.yml` enforces idiomatic Go: `errcheck`, `errorlint`,
`staticcheck`, `gosec`, `revive`, and others. Fix all warnings before opening a PR.

## House style

A few decisions that aren't enforced by tooling but are expected for new code.
Existing code that pre-dates a rule may not conform yet — don't churn it as a
drive-by, but do follow these when you touch the file for another reason.

### Constructors and options

Public constructors take variadic functional options:

```go
func New(opts ...Option) *T
func Open(ctx context.Context, opts ...Option) (*T, error)  // when I/O is involved
```

Each package owns its own `Option`/`Options` type — see `magus.Option`,
`cache.Option`, `types.SpellOption`. Internal packages may use a positional
config struct (`New(opts Options)`) when the constructor is called from
exactly one place; promoting one to public means converting to functional
options.

### Logging

Library code logs via `log/slog`. Reserve `fmt.Fprintf(os.Stderr, …)` for
the `cmd/magus` CLI itself, where output is part of the user contract.
Adapter packages under `vcs/`, `internal/cache/`, etc. should log structured
events so the daemon and CLI can route them consistently.

### File writes

Files written inside a magus-managed directory go through
`internal/util.WriteFileAtomic`, never bare `os.WriteFile` or hand-rolled
temp+rename. The helper handles `fsync` so a crash mid-write doesn't leave
a truncated cache entry.

### JSON

Use `internal/util.Marshal` / `Unmarshal` / `NewEncoder` / `NewDecoder`
instead of importing `encoding/json` directly. The wrapper exists so the
codec can be swapped under a build tag (the v2 transition).

### Lookups

In-memory map-style reads return `(T, bool)` with `Lookup` as the target;
reserve `Get` for "must exist or it's a bug" reads that don't return an
ok flag. I/O-backed reads return `(zero, error)` wrapping `magus.ErrNotFound`
or `os.ErrNotExist`. Don't mix the two in the same store.

### Context

Any method that may touch disk, network, or a subprocess takes a
`context.Context`. Pure metadata reads can skip it.

### Package layout

Package names are lowercase with no underscores and match their directory
segment where practical — `bindgen`, not `bind_gen`. Place a package _under_
what it composes rather than beside it: the sandbox-policy builder lives at
`internal/sandbox/policy` (package `policy`), next to the `sandbox/env` and
`sandbox/filesystem` packages it draws on, not as a sibling of `sandbox`. No
alias should ever be _required_ to disambiguate an import; two packages may
share a short name when they are never co-imported and the names are
accurate — `config/env` and `sandbox/env` are both `package env`
(config-derived flag/var names vs. the sandbox env allowlist) and are left
as-is.

## Performance

Benchmark conventions for magus. The overarching rule: every
performance-related change must carry a checked-in `Benchmark*` plus
benchstat evidence — speculative micro-opts are rejected.

### Startup latency

In-process benchmarks live in
[`cmd/magus/startup_bench_test.go`](cmd/magus/startup_bench_test.go).
Ground-truth wall-clock measurement uses
[`hack/bench_startup.sh`](hack/bench_startup.sh).

#### Capture baselines

```bash
# In-process. Run each bench in isolation — running them together pollutes
# the first one with cache-package-init costs that fire once per `go test`
# binary.
for b in BenchmarkStartupHelp BenchmarkStartupVersion \
         BenchmarkStartupCompletionBash BenchmarkStartupLs; do
  go test -run=^$ -bench=^${b}$ -benchmem -benchtime=2s -count=10 \
    ./magus/cmd/magus
done > /tmp/bench.before.txt

# Spawn-based (true cold start — includes Go runtime init).
hack/bench_startup.sh > /tmp/spawn.before.txt
```

After your change, repeat with `*.after.txt` and compare:

```bash
benchstat /tmp/bench.before.txt /tmp/bench.after.txt
```

Paste the relevant rows into the PR description and the `optimization:`
comment. **Do not commit** `bench.before.txt` / `bench.after.txt` —
they belong in the commit message, not the tree.

#### The `optimization:` comment

Every micro-optimization gets an inline `optimization:` comment in the
form established by
[`internal/cache/mtime.go:36`](internal/cache/mtime.go):

```go
// optimization: <what changed in one line>.
//   measured: <BenchmarkName> <delta> (benchstat, n=N).
//   trade-off: <legibility/portability cost>.
//   assumes: <platform/kernel/build constraint>.
```

A reviewer reading the comment should be able to evaluate the trade-off
without re-running the bench.

#### Platform fast-path pattern

Per-OS optimizations use Go build tags as in
[`internal/cache/reflink/`](internal/cache/reflink/) — `_linux.go`,
`_linux_other.go`, `_other.go` files with `//go:build` constraints. A
portable fallback MUST exist; never gate functionality on a fast path.

### CI bench job

The `magus-bench` job in `.github/workflows/ci.yaml` runs
`go test -bench=. -benchtime=2s` across the packages listed in the
job — currently `internal/cache`, `internal/report`,
`internal/ci/forecast`, and `cmd/magus`. The job restores a baseline
from `main` for PR comparison and surfaces the benchstat output in the
step log. The comparison is non-blocking; GHA runners are too noisy to
gate merges on the delta alone.

## Pull requests

- One logical change per PR.
- Add or update tests for every behaviour change.
- Run `go test -race ./...` before pushing — the CI suite does the same.
- Keep commit messages in the imperative mood ("Add X", "Fix Y").
