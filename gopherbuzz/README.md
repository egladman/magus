# buzz

A pure-Go bytecode VM for the [Buzz](https://buzz-lang.dev/0.5.0/) scripting
language: lexed, parsed, type-checked, compiled to a flat instruction stream, and
run on a register-window VM with an 8-byte NaN-boxed value. Hot top-level numeric
loops are compiled to native code by an opt-out **baseline JIT** (amd64). Embed
via `NewSession`.

- Reference: <https://buzz-lang.dev/0.5.0/reference/> · API: `doc.go`
- Hot-path notes: [Performance design](#performance-design) · JIT: [Baseline JIT](#baseline-jit)

## Performance

Two workloads vs other Go-embedded languages, each compiled once and run per
iteration (benchstat median, n=6, amd64 Xeon @ 2.80 GHz, Go 1.25). `LoopSum` sums
`0..1e6`; `Fib` is recursive `fib(30)` (call-heavy, so Buzz runs it on the
interpreter — JIT'd calls aren't done yet). Harness: `benchmarks/comparison/`.

| Engine | LoopSum | Fib(30) | LoopSum mem |
|---|--:|--:|--:|
| **Buzz (JIT)** | **5.9 ms** | 188 ms | 5.7 KB |
| **Buzz (interp)** | 36.7 ms | **182 ms** | 2.0 KB |
| gopher-lua | 47.6 ms | 266 ms | 15 MB |
| tengo | 78.3 ms | 217 ms | 15 MB |
| goja (JS) | 395 ms | 403 ms | 107 MB |

Buzz leads both — ~8× over gopher-lua and ~13× over tengo on the loop, and the
interpreter even wins `fib` — at KB, not MB/GB, of allocation. Cross-language
microbenchmarks differ in semantics; read as order-of-magnitude.

Reproduce:

```sh
go test -run='^$' -bench=. -benchmem ./...                # in-tree (BUZZ_JIT=0 for interp)
cd benchmarks/comparison && GOWORK=off go test -bench=. . # cross-language
```

## Building

```sh
go build ./...
go test ./...
```

No cgo, no external toolchain. Pure-Go deps:
[`purego`](https://github.com/ebitengine/purego) (`zdef()` FFI) and
[`golang-asm`](https://github.com/twitchyliquid64/golang-asm) (JIT codegen, amd64).

`default.pgo` is applied automatically by Go 1.21+ when building from this dir;
regenerate with `magus run regen-pgo gopherbuzz` after hot-path changes (a stale
profile is neutral). After bumping `BytecodeVersion`, run `go generate` in
`../internal/spell` to rebuild the embedded spell bytecode.

## Build tags

Three mutually exclusive `Value` representations; one is compiled at a time.

| Tag | `Value` | Use |
|---|---|---|
| _(none)_ | 8-byte NaN-box + handle table | **default production build** |
| `buzz_safe` | 24-byte interface + assertion, bounds-checked | CI / differential testing |
| `buzz_unsafe` | 24-byte pointer struct | legacy baseline |

The default build has **zero GC write barriers** on the push/arith/pop path (the
operand stack is `[]uint64`). `buzz_safe` is behaviorally identical and slower —
it lets CI validate the fast build. The [JIT](#baseline-jit) is built with the
default rep on amd64 and arm64; every other config (safe/unsafe, other arches,
wasm) uses a no-op stub.

```sh
go test -tags buzz_safe ./...
go test -tags buzz_unsafe ./...
```

## WebAssembly

The core is pure Go with no cgo, so it cross-compiles to wasm unmodified
(`zdef()` returns "unsupported"; the JIT uses its stub). `wasm/main.go` (guarded
by `//go:build wasm`) reads a program from stdin and prints a trailing `return`:

```sh
tinygo build -target=wasi -o buzz.wasm ./wasm        # ~1.6 MB; default scheduler (fibers use goroutines)
GOOS=wasip1 GOARCH=wasm go build -o buzz.wasm ./wasm # ~4 MB, no extra toolchain
echo 'return (1 + 2) * 10;' | wasmtime buzz.wasm     # 30
```

Both `wasip1/wasm` and `js/wasm` build. This makes gopherbuzz (to our knowledge
the first Go implementation of Buzz) run **in the browser** — the magus docs
site's Buzz playground (`magus/cmd/buzz-playground` over `magus/internal/playground`)
evaluates Buzz live and dry-runs a `magusfile.bzz`, with host calls recorded.

## Architecture

```
source → Parse → ast.Program → Checker → Compiler (FoldConsts → FusePeephole)
       → Chunk (bytecode) → VM.Exec (register-window stack) → Value
```

- **`Instr`** `{Op uint8, A, B int32}` — word-coded, pointer-free, in a
  contiguous slice, fetched without bounds checks on the hot path.
- **`Value`** — 8-byte NaN-boxed word. Immediates (int/float/bool/null) live in
  the payload; heap objects are indices into a per-VM handle table, so the
  operand stack is `[]uint64` with no GC-visible pointers.

## Baseline JIT

On **amd64**, a hot top-level chunk whose body is the numeric loop/arithmetic
opcode subset is compiled to native code, deleting interpreter dispatch. On by
default; disable with `BUZZ_JIT=0` or `vm.SetJIT(false)`.

- The pointerless `[]uint64` stack lets native code run with no GC cooperation;
  every value sits at a static slot offset at each opcode boundary, so
  interpreter state is always materialized.
- Each op has an int and a double (SSE) fast path. Anything else — mixed
  int/float, a non-number via `any`, NaN, float ÷0/`%` — **deopts** to the
  interpreter at the recorded ip; unsupported ops (calls, members, strings) make
  the chunk ineligible. The interpreter is the oracle, so the JIT is never wrong.
- Loop back-edges poll cancellation every 256 iterations (one predicted branch).

Codegen uses [`golang-asm`](https://github.com/twitchyliquid64/golang-asm): same
machine code (so same runtime speed) as a hand emitter, but toolchain-verified.
Only the trampolines (`vm/jit_<arch>.s`) are hand asm. Not yet JIT'd: calls,
non-top-level frames, strings.

**amd64** is the runtime-verified, default-on backend (int + float). **arm64**
(`vm/jit_arm64.go`) is int-only and **compile-verified but not yet run on arm64
hardware**, so it's **off by default there** — opt in with `BUZZ_JIT=1` to run the
differential tests on real hardware. Float/div/mod deopt to the interpreter on
arm64. Other arches and the safe/unsafe/wasm builds use a no-op stub.

## Performance design

The interpreter's throughput rests on a few load-bearing tricks. Before touching
the hot path, baseline with `benchstat` over `-bench=. -count=10` and re-check
under `buzz_safe`.

- **`Exec` is I-cache-bound** (~50 KB single `switch`). Adding a new full `case`
  regresses *all* benchmarks 25–55%. Add small branches inside existing handlers,
  or move cold code to `//go:noinline` helpers — never a new case body.
- **Superinstructions** (`FusePeephole`): `OpLocalConstOp`, `OpLocalLocalOp`,
  `OpForCondLC` fuse the dominant `GetLocal/LoadConst/<op>/JumpFalse` patterns.
- **SetLocal absorption**: fused ops peek ahead and write `x = x op y` straight
  to the slot.
- **Static int proof**: bit 31 of a fused op's `B` means "both operands proven
  int" (drops the tag checks); sub-opcode is masked `& 0x7F` / `& 0x7FFF`. Sound
  because `OpCheckType` guards every `any → int` narrowing.
- **Inline caches**: per-VM `mcache` (member access) and field-slot hints
  (`OpGetField`/`OpSetField`) — pointer/index compares, no string scan. Per-VM,
  not per-Chunk (chunks are shared; verified `-race`).
- **NaN-box + handle table**: zero write barriers on push/pop; the table pins
  objects for the VM's life (fine for short per-target sessions).
- **PGO** (`default.pgo`): profile-guided inlining + `OpCall` devirtualization.

## Bytecode version

Bump `vm.BytecodeVersion` (in `vm/marshal.go`) when opcode numbering, the
`Instr`/`Chunk`/`UpvalInfo` layout, the fused-op encoding, or the serializable
`Value`/AST set changes — then `go generate` in `../internal/spell`.

## Contributing gotchas

1. No new `Exec` case bodies (I-cache — see above).
2. Value changes must pass under all three build tags (CI runs default +
   `buzz_safe`; spot-check `buzz_unsafe`).
3. Fused-op sub-opcode masking (`& 0x7F` / `& 0x7FFF`) must track any new flag
   bits, in both `chunk.go` and the VM handlers.
4. `slotTypeInt = 1` (vm `chunk.go`) mirrors `buzz.sInt` — keep in sync.
5. Renumbering opcodes / changing a fused encoding → bump `BytecodeVersion` +
   regenerate spells.
6. `mcache`/`ncache` are per-VM, never per-Chunk (chunks are shared).
7. Re-check escapes with `go build -gcflags='-m=2' ./vm/` after hot-path changes.
