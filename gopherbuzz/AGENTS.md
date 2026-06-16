# AGENTS.md

Guidance for agents working in `gopherbuzz`, the pure-Go reimplementation of the
[Buzz](https://buzz-lang.dev) language that powers magus. Read this before
changing any `.buzz` file or any code under `vm/`, `compiler.go`, `checker.go`,
`parser.go`, `types/`, or `std/`.

## Prime directive: parity with upstream Buzz

gopherbuzz must stay a **strict superset** of upstream Buzz. The same `.buzz`
source has to compile and behave **identically** on both:

- **Upstream Buzz**, the latest unreleased build (currently `0.6.0-dev+049a7de`;
  the exact tag moves, so run `~/.local/bin/buzz --version` to see what you have).
  Binary: `~/.local/bin/buzz`. Source: `~/Repos/buzz`.
- **gopherbuzz**, this tree (`go run ./cmd/buzz`, or the `magus`-built binary).

"Superset" means gopherbuzz may accept MORE than upstream, but it must never
accept LESS, and it must never produce a DIFFERENT result for input that upstream
also accepts. Every change has to preserve that.

Parity has broken before in two directions. Watch for both:

1. **`.buzz` source that only works on one runtime.** Usually it leans on a
   gopherbuzz-only leniency that upstream rejects (see "verify on both" below).
2. **A gopherbuzz VM/compiler change that diverges from upstream semantics** for
   memory, types, or value sharing. Several past regressions came from sharing
   state for the sake of performance: aliasing a value upstream would copy,
   returning a live map instead of a snapshot, a per-Chunk cache that should be
   per-VM. If a change makes gopherbuzz faster by sharing something, you must
   prove the sharing is unobservable.

## Read the upstream Zig source to resolve behavior

Upstream Buzz is implemented in Zig and is cloned at `~/Repos/buzz` (clone
`https://github.com/buzz-language/buzz` there if it is missing). When gopherbuzz
and upstream disagree, or you are chasing a regression or an unfamiliar behavior,
**read the Zig: it is the authoritative spec for what parity means.** The source
is small and greppable, and diffing it against the Go implementation here has been
the fastest way to settle nuances and root-cause regressions.

Keep the clone at the same commit as the binary you test against. The current
`~/.local/bin/buzz` is built from HEAD `049a7de`, which is the `0.6.0-dev+049a7de`
you run, so the source matches the binary exactly.

High-value files (grep by symbol, not line number, since lines drift):

- **`src/Token.zig`** (`keywords`): the reserved-word list, e.g. the `out` regression.
- **`src/FFI.zig`** (`basic_types`): the Zig-to-Buzz FFI type map, plus pointer and
  extern-data-symbol marshalling.
- **`src/Parser.zig`** (`searchZdefLibPaths`): how a `zdef` lib name resolves to a path.
- **`src/Codegen.zig`** (`resolveDynLib`, `ScriptEntryPoint`): when native libs get
  opened (Check does not) and how `main` is invoked.
- **`src/Vm.zig`**: the run flavors (Run vs Check vs Fmt vs Ast) and how they differ.
- **`src/lib/*.buzz`**: the real stdlib surface, what each module actually exports
  (`ffi.buzz`, `buffer.buzz`, `serialize.buzz`, ...).

## Verify every `.buzz` change on BOTH runtimes

gopherbuzz's checker is **deliberately lenient**. Its `-c` silently passes things
upstream rejects (a `var out` named after a reserved word, an unlabeled call
argument, field access on an `any`). So gopherbuzz `-c` is NOT a sufficient gate.

Run, from the directory that holds the script's imports:

```sh
~/.local/bin/buzz -c -L . file.buzz      # upstream: the STRICT gate. Must be clean.
go run ./cmd/buzz   -t -L . file.buzz     # gopherbuzz: run its test blocks
go run ./cmd/buzz      -L . file.buzz     # gopherbuzz: run (auto-calls main())
```

Upstream `-c` (Check) does not open FFI libraries, so it validates syntax and
types without the native libs present. It is the real "does this compile" check.
The canonical dual-runtime example is [`examples/bubblegum/`](examples/bubblegum/);
keep it green on both.

## Known parity hazards

- **Reserved keywords change between Buzz versions.** Upstream now reserves `out`
  (the one that bit us), plus `from`, `pat`, `match`, `test`, and more. A reserved
  word cannot be used as an identifier AT ALL: not as an arg label, not even as a
  plain `var` name. gopherbuzz does not reject these; upstream does. The current
  list lives in `~/Repos/buzz/src/Token.zig` (`keywords`). When you add or rename
  an identifier, check it against that list.
- **Every call argument after the first must be labeled** on upstream, and a bare
  identifier is read as a same-name label: `f(a, b)` means `f(a, b: b)` and errors
  if no param is named `b`. Write `f(a, y: b)`.
- **`any` is not field-accessible upstream** (`E28`); gopherbuzz allows `x.field`
  on an `any`. Type the value, or cast `(x as? T)!`, so both runtimes agree.
- **Value semantics must match.** Upstream copies where gopherbuzz might be tempted
  to alias. Do not "share for speed" across a boundary upstream treats as a copy.

## Memory and pointer discipline (the silent-corruption trap)

This is the bug class that sends you on a multi-session wild goose chase: it
corrupts memory far from where the mistake is and crashes intermittently (ASLR
makes it look random) until you finally trace it back.

- The **default `Value` is an 8-byte NaN-box with a 48-bit immediate payload**
  (`±2^47`; see `vm/value_nanbox.go`). A 64-bit pointer or a large integer does
  **not fit**, and it **silently truncates** if you stuff it into an int Value.
  The truncated bits later get dereferenced as an address and corrupt the heap.
  This is exactly the FFI handle-truncation bug that crashed `bubblegum`, fixed by
  the heap-boxed `ud` value (`UDValue` in `vm/value.go`). Never put a raw 64-bit
  pointer in an int; route FFI pointers through `ud`.
- The `Value` type has **three representations, one per build tag**: the 8-byte
  NaN-box (default), a 24-byte interface (`buzz_safe`), and a 24-byte pointer
  struct (`buzz_unsafe`). Any change to `Value` MUST pass under all three:

  ```sh
  go test ./...
  go test -tags buzz_safe ./...
  go test -tags buzz_unsafe ./...
  ```

  `buzz_safe` is bounds-checked and behaviorally identical to the fast build; it
  exists to catch exactly the kind of out-of-bounds read/write the NaN-box path
  cannot.

## Performance: benchmark before and after any hot-path change

The interpreter, JIT, superinstructions, and inline caches are regression-prone:
a "harmless" refactor can quietly cost 25-55% across the board. Before AND after
any change to `vm/` dispatch, the opcode handlers, the checker, or the compiler:

- **Never add a new `case` to the `Exec` switch.** It is I-cache-bound; a new case
  body regresses ALL benchmarks 25-55%. Add a branch inside an existing handler,
  or a `//go:noinline` helper, instead.
- Benchmark with `benchstat` over `-count=10`, and re-check under `buzz_safe`:

  ```sh
  go test -run='^$' -bench=. -benchmem -count=10 ./...
  ```

- Regenerate PGO after material hot-path changes: `magus run pgo-generate gopherbuzz`.
  (A stale profile is only neutral, not wrong, but keep it fresh.)

## Build, test, lint (prefer magus)

This is a magus workspace; route through magus:

```sh
magus run ci gopherbuzz       # lint + build + test (the affected anchor)
magus run test gopherbuzz
magus run build gopherbuzz
```

The build-tag and benchmark commands above use raw `go test` because they target
specific tags and benches the `ci` target does not split out.
