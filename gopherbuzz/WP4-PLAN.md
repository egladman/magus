# WP4 — 8-byte value representation (NaN-box + pointerless stack)

Detailed execution plan for the remaining work of `[A1]` in `PERFORMANCE.md`. WP4
part 1 (the representation seam) has landed; this document plans parts 2–4. It is
written to be picked up cold in a future session.

## 1. Goal and measured payoff

Replace the 24-byte `Value{t, n, obj}` (`value_unsafe.go`) with a single **8-byte
word** so the operand stack, register windows, and frames become **pointerless** —
no GC write barriers on the hot push/arith/pop path, a third of the copy traffic,
and register/SIMD-friendly values.

De-risking already measured (recorded in `PERFORMANCE.md [A1]`, WP4 part 1):

| Signal | Value |
| --- | --- |
| `Value` size today | 24 bytes |
| `gcWriteBarrier` refs in `Exec` | 472 |
| Isolated push/arith/pop, 24-byte pointer value vs 8-byte `uint64` | 910ns → 432ns (**~2.1×**) |

The 2.1× is the ceiling for the stack-traffic portion of a benchmark, not the
whole benchmark; expect the largest real wins on barrier- and copy-heavy loops
(LoopSum, Fib, LoopEq).

## 2. The hard constraint (why this is atomic and multi-session)

The `asX()` accessors (`asStr`, `asList`, `asMap`, `asFun`, …) are methods on
`Value` with **no VM/session context**. They work today only because the Go
pointer is stored *in* the value. An 8-byte value cannot carry a GC-visible
pointer (a pointer hidden in a `uint64` is invisible to the collector → use-after-
free), so a heap reference must become an **index/offset** resolved against a
per-session table or arena.

That per-session table **cannot be global** — each magus target runs its own
`Session`/VM on its own goroutine, and the fiber work depends on that isolation; a
shared table would race and break it. Therefore the resolving context must thread
through the value-access API. This is the shared, unavoidable change, and it is
large:

| Seam | Call sites (measured) | Why context is needed |
| --- | --- | --- |
| `v.asStr()` → `vm.asStr(v)` (all `asX`) | **115** | resolve index → object via the per-VM table |
| `heapValue(tag, ptr)` → `vm.alloc…` | **23** | allocation must intern into the per-VM table |
| helper threading in `operators.go` | **~18 funcs** | `arith`/`compare`/`getMember`/… take `Value`s, call `asX`/`heapValue`, currently have no `vm`; `vm` must thread through the whole helper call graph at once |

(Counts from `grep -E '\.as(Str|List|Map|…)\(\)'` and `heapValue(` over the vm
package, this session.) Because the helpers call each other, the threading is not a
mechanical `sed` like the `tag`/`num` seam was — it is a coordinated refactor that
must land in one compiling step or the default build breaks.

## 3. Recommended representation: handle table first, arena later

Two designs resolve a heap index; they differ in where heap **objects** live.

### 3a. Handle table (recommended first target)

- Heap objects stay as **normal Go structs** in a per-VM table
  (`heap []heapObj` or typed pools). The table is a normal Go slice → the GC scans
  it → objects stay alive.
- An 8-byte `Value` heap ref is an **index into that table**.
- The **stack/registers hold only indices and immediates → pointerless → no
  barriers** on the hot path. The 2.1× stack-traffic win is captured here.
- `asX` becomes `vm.asStr(v)` = `vm.heap[v.idx].(*strObj)` (or a typed pool load).

**Trade-off — memory:** the table pins every heap object for the VM's life; Go's
GC can no longer free an object the program dropped. Acceptable for magus's
short-lived per-target sessions (the bump/arena note in `dropWindowThreshold`
already makes this trade for stack slots). For long-running embedders, mitigate
with generational table compaction at a safe point (no live frame holds a raw
index), or weak slots. **Decide and document before flipping the default.**

This design captures the barrier-elimination win **without** rewriting strings/
lists/maps — they remain Go objects. It is the pragmatic milestone.

### 3b. Full arena, pointerless heap objects (later — JIT substrate)

Strings/lists/maps stored as **arena bytes/offsets**, copied out at the embedding
boundary. This is what `[S1]` (baseline JIT) ultimately needs — native code cannot
hold Go-managed pointers — but it is strictly more work (every heap type rewritten,
embedding-boundary marshalling) and is **not** required for the interpreter win.
Defer until the JIT is on the table.

## 4. Milestones

Each milestone is independently committable and green on both existing build tags
plus the new one. **Discipline: never push a non-compiling default build.** Do the
work in the working tree; commit only green.

### M2 — `asX`/`heapValue` VM-context seam  *(shared; unblocks everything)*

1. Change `asX` accessors to `(vm *VM) asStr(v Value) *strObj` etc. The current
   reps (`value_unsafe.go`, `value_safe.go`) implement them trivially (ignore
   `vm`, cast the in-value pointer) — proven zero-cost by inlining, the same way
   `tag()`/`num()` inline today.
2. Change `heapValue(tag, ptr)` call sites to a VM method `vm.allocStr(...)` etc.;
   default/safe reps just box the pointer (no table yet).
3. Thread `vm` through the `operators.go` helper graph (`arith`, `compare`,
   `getMember`, `setMember`, `indexGet`, …). Most callers are inside `Exec` where
   `vm` is the receiver; the rest take it as a parameter.
- **Gate:** both tags compile; full suite + `-race` green; **benchstat shows no
  regression** (the seam must be zero-cost on the existing reps — this is the
  proof that M3 can be added without taxing the default). `.bo` byte-identical
  (no bytecode change).
- **Risk:** highest-churn step. If it cannot land green in one session, it is the
  *only* thing in that session — do not interleave M3.

### M3 — NaN-box rep + handle table behind `buzz_nanbox`

1. Add `value_nanbox.go` (`//go:build buzz_nanbox`); make the existing two rep
   files mutually exclusive with it (`!buzz_safe && !buzz_nanbox`, etc.).
2. NaN-box encoding: doubles stored directly; ints/bool/null/heap-ref in the
   quiet-NaN payload (3 tag bits + 48-bit payload). Heap ref payload = table
   index. **Get the float/NaN edge cases right** (canonicalize incoming NaNs;
   reserve one quiet-NaN pattern space) — this is the fiddliest code; unit-test it
   in isolation first.
3. Per-VM `heap` table + `vm.allocX`/`vm.asX` implementations.
4. Convert the stack/window types to the 8-byte word (likely no signature change
   if `Value` stays the named type, now `uint64`-backed under the tag).
- **Gate:** `go test -tags buzz_nanbox ./...` green; **differential-test** the
  nanbox rep against the safe rep (run the conformance corpus under both, compare
  outputs) — the soundness net. `-race` green. Default build untouched and green
  throughout (files behind the tag are not compiled by default).

### M4 — flip the default + benchstat

1. Make `buzz_nanbox` the default; keep `buzz_safe` as the portable fallback.
2. Full benchstat (n≥10) across the suite; record in `PERFORMANCE.md`. Expect
   large LoopSum/Fib/LoopEq wins from dropped barriers; confirm no OO regression
   (WP3's caches still apply — heap refs are just indices now).
3. Decide the table-memory mitigation (§3a) before this lands if any embedder runs
   long-lived VMs.
- **Gate:** suite + `-race` + downstream magus spell-bytecode parity green; net
  geomean win positive and recorded.

### M5 (optional, JIT-only) — pointerless heap objects (§3b)

Only when `[S1]` is scheduled. Out of scope for the interpreter.

## 5. Rollback / safety

- M2 is the only step that touches the default build; it is reversible by revert
  (no data/format change). M3 lives entirely behind a tag and cannot break the
  default. M4's flip is a one-line build-constraint change, trivially reverted.
- No `.bo`/marshal/version change anywhere in M2–M4 (the value representation is
  runtime-only; bytecode is unaffected). Confirm `.bo` byte-identical at each gate.

## 6. Success criteria

- Default `Value` is 8 bytes; `Exec` carries **zero** `gcWriteBarrier` refs on the
  push/arith/pop path (verify by `go build -gcflags=-d=ssa/check_bce` / barrier
  inspection per `PERFORMANCE.md` "GC write barriers").
- Benchstat: positive geomean across the suite, large LoopSum/Fib wins, no OO
  regression, recorded with n≥10.
- `buzz_safe` and `buzz_nanbox` differential-test identical on the conformance
  corpus; `-race` clean; downstream spell parity green.
