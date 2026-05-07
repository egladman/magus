# buzz

A stack-based bytecode interpreter for the [Buzz](https://buzz-lang.dev/0.5.0/)
scripting language, written in pure Go. Source is lexed, parsed, type-checked,
compiled to a flat instruction stream, and executed by a register-window VM. The
primary embedding entry point is `NewSession`.

- Language reference: <https://buzz-lang.dev/0.5.0/reference/>
- API: see `doc.go` and the package godoc
- Performance roadmap: `PERFORMANCE.md`

---

## Building

```sh
go build ./...
go test ./...
```

No cgo, no external toolchain. The only optional dependency is
[`purego`](https://github.com/ebitengine/purego) for `zdef()` FFI, which is
compiled only on platforms where purego supports it; on all others the
interpreter builds and runs fully and `zdef()` returns a clear error.

### PGO

A CPU profile lives at `default.pgo`. Go 1.21+ applies it automatically when
building from this directory. Regenerate it after material changes to the VM hot
path:

```sh
magus run regen-pgo gopherbuzz
```

A stale profile drifts toward neutral (it won't hurt, but it won't help). Rebuild
the embedded spell bytecode after changing `BytecodeVersion`:

```sh
cd ../internal/spell && go generate
```

---

## Build tags

Three mutually exclusive representations of `Value` are available. Only one is
compiled at a time.

| Tag _(default: none)_ | `Value` | Instruction fetch | Use |
|---|---|---|---|
| _(none)_ | 8-byte NaN-box + handle table (`value_nanbox.go`) | unchecked (`fetch_nanbox.go`) | **Default production build** |
| `buzz_safe` | 24-byte interface + type assertion | bounds-checked | CI verification, differential testing |
| `buzz_unsafe` | 24-byte pointer struct | unchecked | Legacy / comparison baseline |

The default NaN-box build carries **zero GC write barriers** on the push/arith/pop
path (the operand stack is `[]uint64`, pointer-free). `buzz_safe` is behaviorally
identical and slower; it exists so CI can validate the fast build's invariants.

```sh
go test -tags buzz_safe ./...   # safe twin
go test -tags buzz_unsafe ./... # old unsafe rep
```

---

## VM architecture overview

```
source text
    │ Parse
    ▼
ast.Program
    │ Checker  (type annotations, gradual typing)
    │ Compiler (emits Chunk: []Instr + const pool + nested Funs)
    │ FoldConsts → FusePeephole
    ▼
Chunk (bytecode)
    │ VM.Exec  (register-window stack; frame.base as register file)
    ▼
Value
```

**`Instr`** is `{Op uint8, A int32, B int32}` — a word-coded, pointer-free struct
in a contiguous slice, fetched without bounds checks on the hot path.

**`Value`** is an 8-byte NaN-boxed word (default). Integers, floats, booleans,
and null are encoded directly in the payload bits; heap objects (strings, lists,
maps, closures, …) are stored as indices into a per-VM handle table, so the
operand stack is `[]uint64` with no GC-visible pointers and no write barriers.

---

## Performance design

The interpreter achieves its throughput through a layered set of optimizations.
Understanding them is essential before modifying the hot path.

### 1 — The Exec I-cache constraint

`VM.Exec` is a single ~50 KB function (one `switch` over 60+ opcodes). The L1
instruction cache on typical x86-64 / ARM64 hardware is 32–64 KB. The entire
hot-path dispatch loop must stay I-cache resident.

**Critical rule: adding a new, full-sized `case` handler to `Exec` regresses ALL
benchmarks by 25–55%, even benchmarks that never execute that opcode.** The
displacement of the hot handlers from L1 is the cause. Verified experimentally;
see the `OpLocalConstStore`/`OpLocalLocalStore` and `OpReturnLocal` attempts in
the session history.

Safe patterns that do not trigger this regression:
- Add a small branch (≤ ~80 bytes) **inside an existing handler** — e.g. the
  SetLocal absorption in `OpLocalConstOp`/`OpLocalLocalOp`.
- Move cold code to a `//go:noinline` helper — the hot/cold split in
  `errUndefinedVar` et al. saves ~2 KB from Exec's text.

Unsafe: adding any new case body to the switch. Measure with `benchstat` over the
full suite before and after.

### 2 — Superinstructions (`FusePeephole`)

`FoldConsts` + `FusePeephole` run immediately after compilation. Three fused ops
cover the dominant instruction patterns:

| Superinstruction | Fuses | Saves |
|---|---|---|
| `OpLocalConstOp` | `GetLocal; LoadConst; <binop>` | 2 dispatches, 2 push/pop |
| `OpLocalLocalOp` | `GetLocal; GetLocal; <binop>` | 2 dispatches, 2 push/pop |
| `OpForCondLC` | `GetLocal; LoadConst; <cmp>; JumpFalse` | 3 dispatches |

Jump operands are absolute indices, so fusion rewrites in-place (super + `OpNop`
fill) rather than collapsing the stream. A window is suppressed if any branch
targets a slot inside it.

### 3 — SetLocal absorption

`OpLocalConstOp` and `OpLocalLocalOp` peek at the immediately following
instruction. If it is `OpSetLocal` back to the same slot (the `x = x + y`
pattern), the result is written directly to the stack slot, absorbing the
`OpSetLocal` dispatch and the push/pop round-trip. This is a runtime lookahead
inside existing handlers — not a new opcode.

### 4 — Static int-type proof (bit 31 of fused op `B`)

`OpGetLocal` carries the slot's static type (from the compiler's `styp` lattice)
in its `B` field. `FusePeephole` sets **bit 31** of the fused instruction's `B`
field when it can prove both operands are `int` at compile time:

- `OpLocalConstOp`: left slot is `sInt` and the constant is `tagInt`
- `OpLocalLocalOp`: both slots are `sInt`

The VM handler checks `ins.B < 0` (sign bit) and, when set, skips the two
runtime `tag == tagInt` comparisons entirely. Sound because `OpCheckType` is
emitted at every `any → int` narrowing, so a proven-int slot always holds an
int at runtime.

**Encoding note:** bit 31 bleeds into the sub-opcode byte. Both handlers mask it
off: `uint32(ins.B) >> 24 & 0x7F` (LocalConstOp) and `uint32(ins.B) >> 16 &
0x7FFF` (LocalLocalOp). If you ever widen the sub-opcode range beyond 7 bits /
15 bits, re-examine this masking.

### 5 — Runtime member-access inline cache (`mcache`)

`OpGetMember` / `OpSetMember` maintain a per-VM `[]mcacheEntry` indexed by
instruction position. Each entry stores a `*objectDefObj` pointer and a field
index. A hit is a pointer-equality check (`e.def == inst.Def`) — no string
comparison. The cache grows monotonically and is never reset; correctness rests
entirely on the read-side verify.

The cache is **per-VM** (not per-Chunk), so concurrent VMs sharing a `*Chunk`
don't race. Verified under `go test -race`.

### 6 — Compile-time field slots (`OpGetField` / `OpSetField`)

Inside method bodies, `this.field` accesses are compiled to `OpGetField`/`OpSetField`
with a compile-time field-index hint (A) and a fallback name (B). The handler
checks `Keys[hint] == name` (one comparison) and, on a hit, does a direct
`Fields[hint]` load — no map scan. On a miss it falls back to the name path
and is sound for any receiver.

### 7 — PGO

`default.pgo` provides profile-guided inlining and interface-call devirtualization
(`Callable` in `OpCall`). Re-run `magus run regen-pgo gopherbuzz` after material changes to the VM
dispatch order or opcode set. A stale profile is neutral; it won't introduce
regressions, but it won't help either.

### 8 — NaN-box and the handle table

The default `Value` is a single `uint64`. Doubles are stored directly; ints,
bools, null, and heap references are encoded in the quiet-NaN payload. Heap refs
are indices into a per-VM `heap []heapVal` table — not raw pointers — so the
operand stack is a `[]uint64` with no pointers, triggering **zero GC write
barriers** on push/pop. The GC scans only the table itself.

Trade-off: the table pins objects for the VM's lifetime (Go's GC cannot
collect a heap entry). Acceptable for magus's short-lived per-target sessions.

---

## Bytecode version

`vm.BytecodeVersion` (in `vm/marshal.go`) must be incremented whenever:
- opcode numbering changes (`opcode.go`),
- the `Instr` / `Chunk` / `UpvalInfo` layout changes,
- the fused-instruction encoding changes (e.g., the bit-31 int-type flag added
  in v6), or
- the serializable `Value` subset or AST node types change.

After bumping the version, regenerate the embedded spell bytecode:

```sh
cd ../internal/spell && go generate
```

---

## Contributing — things to watch out for

**Before touching the hot path**, run a full benchmark baseline:

```sh
go test -run='^$' -bench=. -benchmem -benchtime=300ms -count=10 \
    -pgo=./default.pgo ./... > /tmp/before.txt
# make your change
go test -run='^$' -bench=. -benchmem -benchtime=300ms -count=10 \
    -pgo=./default.pgo ./... > /tmp/after.txt
benchstat /tmp/before.txt /tmp/after.txt
```

Key things that will surprise you:

1. **I-cache budget is exhausted.** Any new `case` body in `Exec`'s switch will
   regress every benchmark. Add logic inside existing handlers instead, or plan a
   structural split first.

2. **Three `Value` reps.** Every change to `value.go`, `operators.go`, or any
   VM helper that touches values must compile and pass tests under all three
   tags. The CI runs `buzz_safe` and the default; spot-check `buzz_unsafe` when
   touching representation code.

3. **Sub-opcode masking in fused handlers.** `OpLocalConstOp` and `OpLocalLocalOp`
   use bit 31 of `B` as a type flag. The sub-opcode is extracted with `& 0x7F`
   / `& 0x7FFF` masks. If you add more flag bits, update both FusePeephole
   (chunk.go) and the VM handlers.

4. **`slotTypeInt = 1` is a cross-package constant.** `chunk.go` (vm package)
   uses the literal `1` to represent `buzz.sInt`. If `buzz.sInt` ever changes
   value, update both.

5. **Bytecode is versioned.** Adding or renumbering opcodes, or changing a fused
   instruction's bit encoding, requires bumping `BytecodeVersion` and running
   `go generate` in `../internal/spell`.

6. **PGO profile goes stale.** After adding or reordering opcodes, re-run
   `magus run regen-pgo gopherbuzz`. A stale profile is neutral but a freshly regenerated one
   often recovers 5–20% on `Call`/`FieldAccess`/`MethodCall`.

7. **`mcache` and `ncache` are per-VM, not per-Chunk.** Chunks are immutable and
   shared across goroutines. Any instruction-position-indexed side data must live
   on the `VM`, not the `Chunk`.

8. **Escape analysis.** Run `go build -gcflags='-m=2' ./vm/` after hot-path
   changes and check that `Value` stays a value type (not heap-allocated), that
   `frame` doesn't escape, and that the error-path helpers (`errUndefinedVar`,
   etc.) remain `noinline`. A single stray escape on the hot path is measurable.
