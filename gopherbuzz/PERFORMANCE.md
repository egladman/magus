# Buzz VM Performance Checklist

A prioritized, deeply technical optimization roadmap for the Buzz VM, targeting
**LuaJIT-class throughput**. Ranked by *effort-adjusted expected impact* for this
specific implementation, informed by the Deegen / LuaJIT-Remake write-ups
([interpreter](https://sillycross.github.io/2022/11/22/2022-11-22/),
[baseline JIT](https://sillycross.github.io/2023/05/12/2023-05-12/)), and by the
production techniques of LuaJIT, V8, JavaScriptCore, SpiderMonkey, HotSpot, and
PyPy.

This is a strategy document, not a task list. Each entry is scoped to what
actually moves the needle here; cargo-cult micro-opts are deliberately excluded.

---

## 0. Strategic framing — read this first

Three structural facts determine the whole ranking. Ignore them and you will
spend months on optimizations that cannot reach the target.

### Fact 1 — there is no JIT yet; this is a tuned bytecode *interpreter*

`magus/gopherbuzz/vm/` is a stack-based bytecode interpreter with a `switch` dispatch
loop (`vm.go:264`), an 8-byte NaN-boxed `Value` (`value_nanbox.go`; 24-byte
fallback under `-tags buzz_unsafe`), checked instruction fetch (`fetch_nanbox.go`;
unchecked under `-tags buzz_unsafe`), an inline name cache (`ncache`),
and small-object maps (`mapObj`). It is already well past the naive level. But
**no amount of interpreter polishing reaches LuaJIT**, because LuaJIT's *baseline*
is a tracing JIT emitting native code. The headline items below (S-tier) are
about emitting machine code; everything else is about (a) raising the interpreter
floor and (b) building the substrate the JIT needs.

### Fact 2 — Buzz is statically typed; exploit that instead of imitating dynamic VMs

Most of the marquee techniques in the brief — **hidden classes/shapes, runtime
inline caches, quickening, NaN-box type tags, speculative type checks +
deopt** — exist to *recover, at runtime, type information that dynamic languages
discarded*. Buzz's checker (`checker.go`, `types.Type`) already has it at compile
time. Resolving a field offset, a method slot, or an arithmetic specialization in
the compiler is **strictly faster and far simpler** than discovering it with an
IC and guarding it with a deopt. The single largest under-exploited asset in this
codebase is the type checker. The priority order below leans hard into
compile-time specialization and treats runtime speculation as a fallback only for
genuinely polymorphic sites (`any`, unions, host/FFI boundaries).

### Fact 3 — the Go runtime is the ceiling, and it dictates the value representation

The Deegen interpreter's speed comes from things Go *cannot express*:

- **Tail-threaded dispatch** where each bytecode handler is a function that
  tail-calls the next, with the VM state (PC, stack pointer, etc.) pinned in
  fixed CPU registers via a custom calling convention (LLVM `GHC`/`preserve_most`).
  Go has no guaranteed tail calls, no computed `goto`, and no control over
  calling convention or register pinning.
- **Per-handler dispatch replication** so each opcode gets its own indirect-branch
  site with its own branch-predictor (BTB) history. Go's `switch` is a *single*
  indirect jump shared by all opcodes — the worst case for the predictor.
- **LLVM-driven hot/cold splitting and block layout.** Go gives you almost no
  layout control.

Consequence: a *pure-Go interpreter* has a hard floor around **3–6× slower than
LuaJIT's interpreter**, no matter how clever. Crossing it means generating native
code that **escapes the Go scheduler and GC**. The moment you do, Go's GC write
barriers and the 24-byte `Value` become the integration problem. That is why
**[A1] value-representation overhaul (NaN-box + arena + pointerless stack)** is
ranked as a *prerequisite* for the JIT, not an independent tweak: it is what makes
native code and Go's GC coexist.

**Bottom line.** Realistic trajectory to the target:

```
Tier 0  interpreter   (today, keep raising the floor: B/C-tier items)
   │    + register bytecode, superinstructions, compile-time specialization
   ▼
Tier 1  baseline JIT  (copy-and-patch; the LuaJIT-class step) ── needs A1 substrate
   │
   ▼
Tier 2  optimizing JIT (SSA IR, type-spec from checker, BCE, OSR, deopt)
```

### 0.1 — The core Deegen lesson, and why our ranking diverges from the dynamic-VM ranking

Deegen's thesis is **not** "shave instructions." Modern CPUs have enormous ILP; a
few extra instructions on a predictable, cache-resident fast path are nearly free.
The lesson is:

> Stop counting instructions. Eliminate **compiler pessimization**, fix **code
> layout**, **specialize execution paths**, keep the **fast path pristine**, and
> **kill unpredictable branches** and cache misses.

Every interpreter item in this document is justified by *that* rubric, not by
instruction count. In particular, where we recommend register bytecode ([A2]) and
superinstructions ([A5]), the win is **not** "fewer instructions executed" — it is
**fewer trips through the single shared, mispredicting indirect dispatch branch**
that Go cannot replicate (Fact 3). That is branch-prediction engineering, the
Deegen rubric, expressed through the one lever Go leaves open. Likewise A4
(hot/cold split), A6 (static specialization), B5 (PGO), and B8 (debug/release
split) are all "keep the fast path pristine + improve layout" — not micro-opts.

**One genuine disagreement with the conventional ranking.** The standard
dynamic-VM order — *(1) hidden classes, (2) inline caches, (3) quickening,
(4) superinstructions, (5) register VM, ...* — is the **correct order for a
dynamically typed language** (it is essentially the LuaJIT-Remake / JS-engine
order). Buzz is **statically typed**, which reorders the top of the list:

| Conventional (dynamic VM) | Buzz (static types) | Why it moves |
|---|---|---|
| 1. Hidden classes/shapes | → compile-time slots **[A3]** | layout is *proven*, no shape, no guard |
| 2. Inline caches | → demoted to `any`/FFI fallback **[B1]** | most sites are monomorphic *at compile time* |
| 3. Quickening | → compile-time specialization **[A6]** | no warm-up, no de-spec, no guard |
| 4. Superinstructions | **[A5]** (still applies) | unchanged — pure dispatch/branch win |
| 5. Register VM | **[A2]** (rises) | the main interpreter lever Go leaves you |
| — | **NaN-box + arena [A1]** (new, high) | GC-write-barrier + JIT-substrate, Go-specific |
| 6. Baseline JIT | **[S1]** (the LuaJIT-class step) | only way past the Go interpreter ceiling |

Hidden classes, runtime ICs, and quickening are *discovery* mechanisms: they learn
at runtime what a dynamic language refused to declare. Buzz declared it. Spending
the engineering budget on shape-guarded ICs when the checker already knows the
shape is rebuilding, at runtime and with deopt risk, something you can emit
directly and guard-free. So for Buzz: **[A3] compile-time slots/vtables outrank
hidden classes; [A6] static specialization outranks quickening; ICs ([B1]) are a
fallback, not the headline.** Everything else in your ranking we keep, and the
fast-path/layout/branch discipline behind all of it is exactly right.

### 0.2 — Things your list raises that Buzz already does (or nearly does)

- **"Never execute a tree / flatten the AST."** Already done — Buzz compiles to a
  flat bytecode `Chunk` (`chunk.go`); the hot path never walks AST nodes. ✔
- **"Single contiguous instruction stream, no pointers in `Instruction`."**
  Already mostly done — `Instr` is a pointer-free `{Op, A, B}` struct in a
  contiguous `[]Instr`, fetched via `unsafe` with no bounds check
  (`fetch_unsafe.go`). Encoding density is the only open question — see [C1]. ✔~
- **"Zero `interface{}`/`reflect` in hot paths."** Already done — the old `Val`
  interface was replaced by the unsafe-pointer `Value` specifically to kill the
  two-word interface dispatch (`value_unsafe.go` header). The one remaining hot
  interface call is `Callable` in `OpCall`, which **[B5] PGO** devirtualizes. ✔~
- **"Avoid maps in hot paths."** Partially — `mapObj` already linear-scans small
  maps to dodge Go-map overhead (`value.go:88`), and **[A3]** removes maps from
  typed object access entirely. ✔~
- **"Intern strings → integer IDs."** Partially — `ncache` resolves names to slots
  and `AddConst` dedups string constants, but runtime map keys and member names
  are still string-compared. **Elevated to [B0] below.**
- **"Cache-line awareness / false sharing."** Already considered — the per-VM
  `cancelN` counter exists specifically to avoid false sharing of a global
  (`vm.go:63`). Extend this discipline to JIT IC/counter structures ([B2]).

These are not gaps; they are evidence the interpreter is already past Tier-0. The
leverage now is the static-typing specialization (A-tier) and the JIT (S-tier).

---

## Impact / difficulty legend

- **Impact**: Tiny · Small · Medium · Large · Massive (geomean throughput on a
  realistic mixed workload, not a single microbench).
- **Difficulty**: 1 (hours) · 2 (days) · 3 (a week+) · 4 (weeks) · 5 (months,
  research-grade).
- **Applies to**: Interp (interpreter) · BJIT (baseline JIT) · OJIT (optimizing
  JIT) · RT (runtime) · GC · OM (object model) · CP (compiler pipeline).

---

# S-TIER — required to reach LuaJIT-class (architectural)

## [S1] Baseline JIT via copy-and-patch

- **What.** Replace the dispatch loop for hot functions with native machine code
  generated by *copy-and-patch* (Xu & Kjølstad, PLDI'21; the technique Deegen's
  baseline JIT uses). Each bytecode handler is pre-compiled **offline** (clang/LLVM,
  at Buzz build time) into a position-independent machine-code *stencil* with
  *holes* for operands, constants, branch targets, and IC pointers. At runtime,
  JITing a function is: walk its bytecode, `memcpy` the stencil for each op into an
  `mmap`'d RWX buffer, and patch the holes. No LLVM at runtime; codegen speed is
  ~interpreter-walk speed.
- **Why.** This is *the* step that crosses the Go interpreter ceiling. It removes
  dispatch entirely (no indirect branch per op), keeps VM state in registers
  *inside* a function's run, and lets adjacent ops' code sit contiguously
  (I-cache wins). Deegen reports baseline-JIT code ~2–4× over an already-fast
  interpreter; over a *Go* interpreter the relative win is larger because the Go
  dispatch you delete is so expensive.
- **Impact:** Massive. **Difficulty:** 5.
- **Applies to:** BJIT, RT, CP.
- **Go-specific (the hard part).** Generated code runs *outside* Go's view, like
  cgo:
  - The Go GC cannot scan JIT stack frames. Values touched by JIT code must hold
    **no Go-managed pointers** → mandates **[A1]** (NaN-box + arena). This is the
    coupling that makes A1 a prerequisite.
  - **Async preemption** (Go 1.14+, SIGURG): the runtime won't preempt a PC it
    doesn't recognize, so long JIT loops block the scheduler/GC. Insert explicit
    safepoint polls at loop back-edges (a load + predicted-not-taken branch into a
    Go trampoline), mirroring the existing `checkCancel` cadence (`vm.go:197`).
  - **Goroutine stacks are movable**; JIT code must run on a fixed stack. Use
    `runtime.LockOSThread` and a dedicated execution stack, entering via a
    trampoline. Calls back into Go (allocation, host `Callable`s, map growth) cross
    a cgo-like boundary — amortized because a JIT'd function executes many ops
    between callbacks.
  - Stencils must follow a fixed register/ABI contract you define (which regs hold
    the Buzz stack pointer, the constant-pool base, the interpreter-state pointer).
- **CPU.** PIC stencils; RIP-relative on x86-64, ADRP/ADD on ARM64. Keep stencils
  branchless internally where possible; emit the dispatch fall-through as straight
  line code so the front-end streams. I-cache pressure shifts from the interpreter
  loop to per-function code — generally a win for hot code, a loss for cold code
  (only JIT hot functions; see [B3] tiering).
- **Alternative.** Hand-written assembler/`DynASM`-style emitter (LuaJIT's route):
  more control, no offline toolchain dependency, but you write an encoder per ISA.
  Copy-and-patch is far less code to reach a working tier-1.

## [S2] Tracing vs. method JIT — choose **method-based + tiering** (decision, not code)

- **What.** Decide the JIT shape *before* S1. LuaJIT is a **tracing** JIT (record
  hot linear traces through loops, optimize the trace, guard at every assumption).
  Deegen/HotSpot/JSC are **method/region** JITs. Recommendation for Buzz:
  **method-based, tiered** (interp → baseline → optimizing).
- **Why.** Tracing's big payoff is collapsing *dynamic* dispatch and type
  dispatch across call boundaries into a flat, type-stable trace. Buzz is
  statically typed, so most of that payoff is already available at compile time
  (Fact 2). Tracing also drags in heavy machinery (trace selection, side exits,
  trace trees, blacklisting) and complex deopt. A method JIT pairs naturally with
  the checker's per-function type info and with copy-and-patch.
- **Impact:** Massive (it gates the entire JIT design). **Difficulty:** n/a
  (decision).
- **Applies to:** BJIT, OJIT, CP.

## [S3] Optimizing JIT: SSA IR + type specialization from the checker

- **What.** Tier-2 compiler for the hottest functions: lower bytecode to an **SSA
  IR**, run classic passes (constant/copy propagation, GVN, LICM, dead-code,
  inlining of small Buzz callees and monomorphic methods), do **bounds-check
  elimination** on `list`/`str` indexing using loop-range facts, **unbox**
  int/float locals into native registers across the function, then emit native
  code (LLVM ORC via cgo, or your own backend).
- **Why.** Baseline JIT removes dispatch; the optimizing tier removes *redundant
  work* — re-boxing, repeated bounds checks, loop-invariant loads, virtual calls.
  This is where HotSpot/V8 get their last several × over baseline. Buzz gets an
  unfair advantage: the checker hands you types, so you skip the speculation +
  guard + deopt dance for everything that is statically monomorphic; speculation is
  reserved for `any`/union/FFI.
- **Impact:** Massive. **Difficulty:** 5.
- **Applies to:** OJIT, CP, RT.
- **Go-specific.** LLVM via cgo gives world-class codegen but per-compile latency
  (acceptable: tier-2 is rare, off the hot path) and a cgo build dependency. A
  hand-rolled linear-scan-register-allocated backend avoids cgo but is a large
  project. Deopt requires a **side table** mapping JIT program points → interpreter
  state (live locals → stack slots) so a failed speculation can reconstruct an
  interpreter frame and resume; design the IR to carry this from day one.

---

# A-TIER — high ROI, mostly achievable in pure Go; substrate for the JIT

## [A1] Value representation: NaN-boxing + arena allocation + pointerless stack

- **What.** Replace the 24-byte `{tag, num, obj unsafe.Pointer}` `Value`
  (`value_unsafe.go:79`) with a single **8-byte NaN-boxed word**: doubles stored
  directly, and ints/bools/null/heap-refs encoded in the quiet-NaN payload space
  (48-bit pointer field on x86-64 and ARM64). Allocate all heap objects from a
  **per-session arena** (bump allocator over `mmap`/`[]byte` blocks, bulk-freed
  when the Session ends), and store heap refs as **arena offsets**, so the operand
  stack (`vm.stack`) becomes `[]uint64` containing **no Go pointers**.
- **Why.** Three wins at once, all double-digit:
  1. **Size.** 24→8 bytes. `vm.stack`, `frame.this`, every `push`/`pop`/
     `replaceTop2`/call copies 1/3 the bytes; 8 values per 64B line instead of ~2.
  2. **GC write barriers vanish.** Today every `push` of a heap value writes a
     pointer into a heap-resident slice → a write-barrier check whenever GC is
     marking. A `[]uint64` stack triggers **zero** write barriers and is **not
     scanned** by the GC at all. This is a large, perpetually-recurring cost
     today that benchmarks (esp. allocation-light loops under concurrent GC) hide
     until you run multi-Session workloads.
  3. **JIT-compatibility.** A pointerless value/stack is what lets native JIT code
     touch the stack without cooperating with Go's GC (see [S1]). This is the real
     reason A1 outranks everything but the JIT itself.
- **Impact:** Large (interp) → Massive (as JIT enabler). **Difficulty:** 4.
- **Applies to:** OM, RT, GC, Interp, BJIT, OJIT.
- **Go-specific.** The arena is the linchpin and it *fits the workload*: the code
  already notes "short-lived per-target sessions magus runs"
  (`vm.go:166`). A region that never frees individually and is dropped wholesale
  at Session end keeps objects alive **without the GC tracing them** — roots are
  the arena blocks themselves (a `[][]byte`), not millions of individual pointers.
  Caveats: strings interned into the arena need care for host interop (copy out at
  the boundary); `unsafe` offset→pointer math must be audited exactly like the
  existing `fetch`/`heapValue` patterns; keep the `buzz_safe` twin (a checked
  interface/handle representation) for differential testing, exactly as
  `value_safe.go` backs `value_unsafe.go` today.
- **CPU.** 8-byte values are register-friendly and SIMD-friendly (see [B6]).
  NaN-box decode is a few ALU ops (mask/compare) — cheaper than today's separate
  `tag` byte load + payload load in most paths, and removes the struct-copy of a
  3-word value. ARM64 and x86-64 both have 48-bit canonical user VAs, so 47–48
  payload bits suffice; **document the assumption** (it breaks on 5-level paging
  >47-bit pointers — arena offsets sidestep this entirely, another reason to
  prefer offsets over raw pointers).
- **Status: started (WP4 part 1).** Representation seam landed; see below.

### [A1] WP4 part 1 — representation seam (zero-cost foundation)

`Value`'s `tag`/`num` are now unexported (`t`/`n`) and read through inlined
`tag()`/`num()` accessor methods; the immediate constructors (`IntValue`,
`FloatValue`, `BoolValue`, `Null`/`True`/`False`) moved into the representation
files (`value_unsafe.go`/`value_safe.go`). All shared VM code now goes through the
accessors, so an alternate representation — the NaN-boxed `uint64` — can compute
`tag`/`num` from bits without touching a single call site. Verified zero-cost: the
accessors inline (cost 3, confirmed inlined into callers), Fib/MethodCall flat at
n=15 (`p=0.967` on MethodCall — WP3's win preserved); both build tags + full suite
green.

**De-risking measured this session:** `Value` is 24 bytes; `Exec` carries **472
`gcWriteBarrier` references**; an isolated push/arith/pop loop on a 24-byte
pointer-carrying value vs an 8-byte pointerless `uint64` ran **910ns → 432ns
(~2.1×)** — confirming the size + barrier payoff.

**The hard core that makes WP4 atomic and multi-session:** the `asX()` accessors
are methods on `Value` with no VM/session context — today they work because the
pointer is *in* the value. A NaN-boxed heap ref is an arena **offset**, which needs
the per-session arena to resolve. A *global* arena would break the per-session
goroutine isolation the whole design (and the fiber work) relies on, so the arena
must thread through the value-access API — i.e. `v.asStr()` becomes
`v.asStr(arena)` (or the VM resolves handles), rippling through every accessor and
opcode at once. That, plus the pointerless heap-object representation (strings/
lists/maps stored as arena bytes/offsets, copied out at the embedding boundary),
is the remaining multi-session work, planned in detail in **`WP4-PLAN.md`**:

1. ✅ Representation seam (WP4 part 1).
2. **M2** — `asX`/`heapValue` VM-context seam (the shared blocker: **115** `asX` +
   **23** `heapValue` sites + ~18-function `operators.go` helper threading, landed
   in one green step). Recommended heap model: a per-VM **handle table** (heap
   objects stay Go-scanned; the *stack* goes pointerless) — captures the barrier
   win without rewriting heap types. Full pointerless-heap arena (§3b of the plan)
   deferred to the JIT.
3. ✅ **M3** — NaN-box `Value` + global handle table behind a `buzz_nanbox` tag,
   differential-tested against the safe rep. `-race` green.
4. ✅ **M4** — flip the default (`!buzz_safe && !buzz_unsafe`); old unsafe rep
   becomes opt-in (`buzz_unsafe`). Lock-free reads on global heap via
   `atomic.Pointer[[]heapVal]`. `exec.go` carries **zero** `gcWriteBarrier` refs
   on the push/arith/pop path (confirmed by `go tool nm`). Benchstat n=10 vs
   `buzz_unsafe` baseline (amd64, Intel Xeon @ 2.80 GHz, Go 1.25):

   | Benchmark            | unsafe (base) | nanbox        | delta       |
   |----------------------|---------------|---------------|-------------|
   | Fib                  | 129.2 ms/op   | 158.0 ms/op   | +22% (↑)    |
   | LoopSum              |  35.7 ms/op   |  43.4 ms/op   | +21% (↑)    |
   | LoopSumShared        |  54.4 ms/op   |  58.1 ms/op   |  +7% (↑)    |
   | LoopEq               |  44.4 ms/op   |  59.2 ms/op   | +33% (↑)    |
   | ForeachList          |  45.1 µs/op   |  31.6 µs/op   | -30% (↓)    |
   | ForeachMap           |   3.6 µs/op   |   2.4 µs/op   | -33% (↓)    |
   | Call                 |   2.5 µs/op   |   0.8 µs/op   | -68% (↓)    |
   | MethodCall           |  14.1 ms/op   |  16.3 ms/op   | +16% (↑)    |
   | FieldAccess          |  66.0 ms/op   |  68.8 ms/op   |  ~ (n.s.)   |
   | DirectCall           |  54.4 ms/op   |  59.9 ms/op   | +10% (↑)    |
   | LoopSumSharedScoped  |  58.1 ms/op   |  58.1 ms/op   |  ~ (n.s.)   |
   | Parse                |  46.0 µs/op   |  26.9 µs/op   | -41% (↓)    |
   | Compile              |  35.4 µs/op   |  16.7 µs/op   | -53% (↓)    |
   | **geomean**          |  **1.552 ms** |  **1.455 ms** | **-6.24%**  |

   Memory geomean: **-31%** (Value shrinks from 24 → 8 bytes; stack is
   `[]uint64`, pointerless). Arithmetic-heavy benchmarks (Fib/LoopSum/LoopEq)
   regress because NaN-box decode adds extra masks/branches per tag check;
   heap-access-heavy paths (ForeachList/Map, Call, Parse, Compile) win from
   lock-free reads and smaller stack copies. Net geomean is positive. ✓

See `WP4-PLAN.md` for milestone gates, the table-memory trade-off, and rollback.

## [A2] Register-based bytecode

- **What.** Convert the stack VM to a **register VM** (Lua 5.x / LuaJIT model):
  operands address virtual registers (the existing `frame.base` window already *is*
  a register file) instead of an implicit operand stack. `OpAdd dst, a, b` instead
  of push/push/add.
- **Why.** In a Go interpreter, **per-dispatch cost is largely fixed** (Fact 3) —
  so the dominant lever you *do* control is **dispatch count**. Register bytecode
  removes the push/pop traffic that exists only to feed the stack: typically
  **30–50% fewer instructions** for arithmetic/expression-heavy code, hence ~that
  many fewer dispatches, fewer `vm.stack` reslices, and fewer `replaceTop2` calls.
  It also makes superinstructions and JIT lowering cleaner (operands are explicit).
- **Impact:** Large. **Difficulty:** 4 (touches compiler codegen + every opcode).
- **Applies to:** Interp, CP, BJIT, OJIT.
- **Go-specific.** `Instr` is already a 3-field word-coded struct (`chunk.go:4`),
  so adding a `C` operand or reusing `A/B` for register indices is mechanical. The
  register window infrastructure (`frame.base`, slot ops, `growWindow`) already
  exists — this is largely a compiler-codegen change plus rewriting opcode bodies
  to read/write `stack[base+r]` instead of pop/push. Pairs with [A1]: wider
  instructions are fine when each value is 8 bytes.
- **CPU.** Fewer back-edges through the shared dispatch branch = fewer
  mispredicts at the one site Go can't replicate.
- **Status: started (Pass 1L/1C).** `Instr.C` landed; `FusePeephole` absorbs
  4-instruction `GetLocal;…;<binop>;SetLocal` windows at compile time. The full
  register-ISA rewrite (all opcodes, not just the fused arithmetic pair) remains.

## [A3] Compile-time field & method slot resolution (kill `mapObj` for typed objects)

- **What.** Today an object instance stores fields in a string-keyed `mapObj`
  (`value.go:111`), and `OpInvoke` resolves methods by `instance.Def.Methods[name]`
  **on every call** (`vm.go:733`). Since field/method sets are statically known per
  object type, have the checker/compiler assign each field a **fixed slot index**
  and each method a **vtable index**. Represent an instance as a flat `[]Value`
  (or arena tuple) + a `*shape`/`*def` pointer. Emit `OpGetField slot` /
  `OpSetField slot` / `OpCallMethod vtableIdx` with indices resolved at compile
  time.
- **Why.** This is the static-typing payoff (Fact 2). It deletes, per access:
  a string hash/scan, a map indirection, and (for objects) the per-instance `mapObj`
  allocation. Field access collapses to a single indexed load with **no shape
  guard** (the type system already proved the layout). Method dispatch collapses to
  an indexed vtable load — no map lookup, no bound-`funObj` allocation. This is the
  equivalent of V8 hidden classes, but *resolved at compile time and guard-free*,
  which hidden classes can never be. Directly targets `BenchmarkFieldAccess` and
  `BenchmarkMethodCall`.
- **Impact:** Large (on OO-heavy code). **Difficulty:** 3.
- **Applies to:** OM, CP, Interp, BJIT.
- **Where you still need runtime ICs.** Only for sites whose static type is
  `types.Any`, a union/supertype, or an FFI/host value — i.e. genuinely
  polymorphic. There, fall back to a **monomorphic inline cache** (cache last
  shape→slot in a side array indexed by instruction), escalating mono→poly(≤4)→
  megamorphic (dictionary), exactly as JSC/V8 do. Keep this path out-of-line so it
  doesn't bloat the hot monomorphic case.
- **CPU.** Flat instance + indexed load is cache-linear; the current small-map
  linear scan is branch-friendly but still O(n) string compares vs. one load.
- **Status: started (WP3/WP4).** `this.field` and typed-local-var slot access landed; see below.

### [A3] WP3 — `this.field` inline-cached slot access landed

`OpGetField`/`OpSetField` (A = field decl-index hint, B = field-name const) replace
the name lookup for `this.field` inside method bodies. They are an **inline cache**:
object field sets are stored in declaration order (`mapObj.Keys`/`Vals` are
insertion-parallel, and `compileObjectLit` emits fields in decl order), so the hint
hits with one key compare and a direct `Vals[hint]` load; a miss (non-canonical
layout or non-object receiver) falls back to `getMember`/`setMember`, so it is
**sound for any value** — no separate shape check needed, and `this` is bound by
dispatch so it is always the object type anyway.

The compiler tracks each object type's field→index map (`thisFields`) when
compiling a method body and emits `OpGetField`/`OpSetField` for `this.<field>`;
non-`this` receivers and nested closures fall back to the name path.

Measured (benchstat n=12, benchtime=300ms, `-pgo=off`): **BenchmarkMethodCall
−19.2%** (p=0.000) — its `dist()` does four `this.field` reads. FieldAccess is
unchanged (its receiver is a top-level global, not `this`, so it stays name-based
— specializing arbitrary typed receivers needs object-type tracking on slots,
the next increment). Allocs unchanged; bytecode version 4→5; embedded spell `.bo`
regenerated.

### [A3] WP3 part 2 — method table (vtable) replaces the method map

`objectDefObj.Methods` changed from a `map[string]*funObj` to an ordered
`[]methodEntry` (declaration order). `OpInvoke` and `getMember` resolve a method
with a small linear scan (`def.method(name)`) instead of a Go map lookup. A
profile of `BenchmarkMethodCall` showed ~6% of runtime in `mapaccess2_faststr` +
`aeshashbody` for `Def.Methods["dist"]` on every call; a handful of methods scan
faster than they hash (no aeshash, no probe) — the same trade-off `mapObj` makes
for small field sets. Measured: **MethodCall −3.5%** (p=0.000, n=12), allocs
unchanged, both build tags + full suite green; runtime-only (no bytecode/version
change). The ordering also gives each method a stable **index**, the foundation
for guard-free index dispatch on a statically-typed `this`/typed-local receiver.

Trade-off: linear scan is O(methods); fine for the typical handful, but a type
with many methods would prefer a lazily-built map above a threshold (as `mapObj`
does for fields) — a noted follow-up.

Still not done (full [A3]): the flat-`[]Value` instance representation (parts 1/2
keep the `mapObj`, just index it), and index-based `this.method()` dispatch (the
vtable index is now available).

### [A3] WP3 part 4 — compile-time field slots for typed local variables

Extends `OpGetField`/`OpSetField` emission beyond `this.field` to any local
variable initialized from a statically-known `TypeName{...}` object literal.

The compiler tracks a `slotObjFields []map[string]int32` (per-slot field-name →
declaration-index) alongside `slotTypes` (primitive-type tracking). When a
`DeclStmt` initializes a slot with an `ObjectLit` whose type is in `typeDecls`,
the field map is recorded. Any reassignment to that slot clears it. A new helper
`localObjFieldIndex(obj, name)` checks whether the receiver of a `MemberExpr`
is a local slot with a tracked type; if so, the compiler emits
`OpGetLocal(slot) + OpGetField(fieldIdx, nameConst)` (read) or
`OpGetLocal(slot) + compileValue + OpSetField(fieldIdx, nameConst)` (write),
bypassing `OpGetMember`/`OpSetMember` and the mcache entirely.

Correctness: the field index is valid for the type because we looked it up at
compile time. The runtime guard `h < len(inst.Fields)` always passes — the slot
holds the correct type (ObjectLit initialization, no reassignment). OpGetField's
existing fallback to the name-based path handles any shape mismatch safely.

No new opcodes, no bytecode version bump, no vm.go changes.

Measured: `BenchmarkFieldAccessLocal` (new benchmark — tight loop on a local
Counter variable inside a fun body): **~12% faster** than `BenchmarkFieldAccess`
(the global-variable mcache path, ~49 ms), at ~43 ms. No regressions in
LoopSum, MethodCall, Fib, ForeachMap, or FieldAccess (global path unchanged).

### [A3] WP3 part 3 — runtime inline cache for `OpGetMember`/`OpSetMember`

Part 1's `OpGetField`/`OpSetField` use a *compile-time* field-index hint, emitted
only for `this.field` where the receiver type is statically known. Field access on
any other receiver — e.g. `c.n` on a global object — falls to the name-scan path
(`mapObj.indexOf`). A `BenchmarkFieldAccess` profile put ~20% of runtime there
(`indexOf` scan 13% + `memeqbody` 10%) across the read and the write of
`c.n = c.n + 1`.

Added a per-VM `mcache []int32` of field-index hints **indexed by instruction
position** (`f.ip-1`). On an object receiver `OpGetMember`/`OpSetMember` reads the
hint, verifies `Keys[hint]==name`, and on a hit does a direct indexed load/store;
a miss scans once (`indexOf`) and learns. It is **grow-only and never reset** —
correctness rides entirely on the read-side verify, so a stale hint (a polymorphic
site, or a different chunk reusing the same ip after a call) merely misses and
relearns and can never return a wrong field. Per-VM, like `ncache`, so concurrent
VMs sharing a `*Chunk` don't race (confirmed under `go test -race`). Field-first
order is preserved (a hit returns the field, exactly as `getMember` would); a name
that isn't a field falls through to `getMember` for method/Null resolution, so map
receivers and method-as-value access are unchanged.

Measured: **FieldAccess −12.3%** (p=0.000, n=10); MethodCall / LoopSum / ForeachMap
unchanged (no regression). Cost: +1 alloc/op in the benchmark — the single cache
array sized once per run in `Run` (reused across runs via a capacity check),
amortized to zero for a reused VM or a long-running program. Purely VM-internal:
no compiler, bytecode, or marshal-version change, so `.bo` output is byte-identical.

This is the runtime-learned generalization of part 1's compile-time hint. It is
sound under dynamic field addition (the verify) — unlike a flat-`[]Value` instance
layout, which would need an overflow map for fields added after construction; the
`mapObj` backing of objects is load-bearing for that semantic, so it stays.

## [A4] Hot/cold splitting of opcode handlers

- **What.** Move every cold path out of the `switch` case bodies into `noinline`
  helper functions: the `fmt.Errorf` error returns in `OpAdd/OpSub/...`
  (`vm.go:352+`), type-mismatch arms, overflow handling, the `arith`/`compare`
  fallbacks. Each hot case becomes: int/float fast path inline, everything else
  `return vm.slowAdd(left,right)`.
- **Why.** This is Go's only real expression of Deegen's hot/cold splitting. Today
  the error-formatting code, string-concat code, and float-promotion code are
  *inline* in the hot cases, inflating the dispatch loop's instruction footprint
  and pushing hot handler bodies apart in the I-cache. `fmt.Errorf` also defeats
  inlining of the whole case. Pulling cold code into `noinline` siblings shrinks
  the hot loop so more handlers stay resident, and lets the compiler keep the fast
  paths tight. Deegen attributes meaningful gains to exactly this.
- **Impact:** Medium. **Difficulty:** 2.
- **Applies to:** Interp, RT.
- **Go-specific.** Go has no `__builtin_expect`, no `cold` attribute, no section
  control — `noinline` helper extraction is the portable substitute for code
  layout. Verify with `go build -gcflags=-m` that the hot case no longer contains
  the `fmt` call graph and that the fast arithmetic paths still inline. Combine
  with [B5] PGO, which orders the cold helpers away from the hot loop in the binary.
- **CPU.** Smaller hot loop → fewer I-cache lines and µop-cache pressure; the
  shared dispatch loop is the single most I-cache-sensitive code in the VM.

## [A5] Superinstructions / instruction fusion

- **What.** Fuse frequent opcode sequences into one. High-value candidates given
  the current ISA: `OpGetLocal a; OpGetLocal b; OpAdd` → `OpAddLL a,b`; the loop
  triad `OpGetLocal; OpLoadConst; OpLess; OpJumpFalse` → a fused
  `OpForCond`; `OpLoadConst; OpAdd` → `OpAddConst`; `OpGetLocal; OpReturn`. You
  already do this opportunistically (`OpInvoke`, `OpBuildStr`, `OpJumpFalsePeek`) —
  systematize it with a peephole pass over emitted bytecode.
- **Why.** Each fusion removes one or more dispatches through the un-replicable Go
  switch — the dominant interpreter cost. It also removes the intermediate
  push/pop the fused op would have done. Cheap to add incrementally; measure each
  against the bench suite and keep only the ones that pay.
- **Impact:** Medium. **Difficulty:** 2–3 (mostly mechanical, one peephole pass +
  N new cases).
- **Applies to:** Interp, CP.
- **Go-specific.** Watch the `switch` size: Go emits a jump table for a dense
  integer switch, so adding cases is cheap *until* the table stops being dense or
  spills the I-cache. Keep opcodes contiguous from 0 (they are) so the jump table
  stays a single indexed indirect jump. Generating the fused cases from a table
  (see [B7]) avoids hand-maintaining dozens of near-identical bodies.
- **CPU.** Fewer back-edges = fewer trips through the one shared mispredicting
  indirect branch.
- **Status: started (WP2).** First superinstruction landed; see below.

### [A5] WP2 — `OpLocalConstOp` landed

`FusePeephole` (vm/chunk.go, run after `FoldConsts` in `CompileWith`) fuses every
`GetLocal; LoadConst; <binop>` triple into one `OpLocalConstOp` — the dominant
shape in loop conditions (`i < N`), induction updates (`i + 1`), and operand
prep (`n - 1`, `n <= 1`, `i % 2`). One dispatch instead of three, and the local +
constant are read directly, eliminating two pushes and two pops.

**Sound under gradual typing** (unlike WP1/WP3): the fused handler runs the *same*
polymorphic op (int fast path + `applyBinop` fallback), so an `any`-tainted
operand behaves identically to the unfused form. No type assumption is made.

Jump operands are absolute instruction indices, so the pass rewrites in place
(super + two `OpNop`) rather than collapsing the stream; the handler does
`f.ip += 2` to skip the nops. A branch may target the *start* of a triple (loop
back-edge to the condition) but the pass suppresses fusion if any branch targets
a triple's *interior*. Bytecode version bumped 2→3 (old binaries reject v3 blobs).

Measured (benchstat n=10, benchtime=300ms, `-pgo=off` both sides):

| bench | Δ | p |
|---|---|---|
| LoopEq | **−26.3%** | 0.000 |
| LoopSum | **−20.7%** | 0.000 |
| Fib | −15.2% | 0.000 |
| FieldAccess | −14.6% | 0.001 |
| MethodCall | −5.1% | 0.000 |
| LoopSumShared / Scoped | ~ (uses OpLoadName, not fused) | — |
| ForeachMap / Call / StringInterp | ~ | — |

geomean −8.8%, allocs unchanged. Exec grew ~34→39 KB; the broad wins confirm no
register-pressure regression (standing rule 3). Next candidates if benchstat
justifies: `GetLocal; GetLocal; <binop>` (LoopSum's `sum + i`) and fusing the
trailing `SetLocal` / `JumpFalse`.

### [A2] WP2 continuation — compile-time register form (Pass 1L/1C) landed

`FusePeephole` now includes two new passes that run before Pass 2:

**Pass 1L** (`GetLocal; GetLocal; <binop>; SetLocal` → `OpLocalLocalOp` with `C=dst+1`):
absorbs a trailing `SetLocal` at compile time, encoding the destination register in
`Instr.C` (0 = stack / push, C>0 → slot C−1). Handles true 3-address operations like
`c = a + b` where dst ≠ src1 — a case runtime SetLocal absorption cannot handle.

**Pass 1C** (`GetLocal; LoadConst; <binop>; SetLocal` → `OpLocalConstOp` with `C=dst+1`):
same idea for the constant-operand variant, covering `sum = sum + i` and `i = i + 1`.

For the existing benchmarks (`sum = sum + i`, `i = i + 1`), Pass 1L/1C fires instead of
Pass 2 + runtime absorption, removing one runtime branch from the hot path (the
`code[f.ip].Op == OpSetLocal` check). For `a = b + c` patterns, it saves an entire
`OpSetLocal` dispatch plus the push/pop round-trip.

`Instr` gains a `C int32` field (zero value = old behavior; no migration needed). The
handlers check `ins.C > 0` once at the top; the predicted-not-taken branch for `C==0`
is near-free. Bytecode version bumped 6→7; spell `.bo` files regenerated.

### WP1 & WP3 — blocked on type soundness (do not ship unguarded)

WP1 (type-specialized arith) and WP3 (object field/method slots) both assume the
checker's static type is a *runtime* guarantee, so they can drop the tag/shape
guard. **It is not.** Buzz is gradually typed: `types.Compat` treats `any` as
bidirectionally compatible, so `any` launders into an `int`/object slot with no
cast and no runtime check. Demonstrated: an `int`-typed var holding a laundered
string makes `i + 1` evaluate to `"hello1"`; the analogous hole lets a
`Counter`-typed var hold a non-object. An unguarded `OpAddInt` / `OpGetField slot`
would reinterpret a heap pointer as an int / shape → unsound (garbage or crash).
A *guarded* specialized op is identical to today's inline fast path → no win.

Measured WP1 ceiling (strip the int tag-check from the hot ops — the most an
unguarded specialization could buy): LoopSum −9.4%, LoopEq −8.7%, Fib ~. Real,
but only reachable unsoundly.

**Prerequisite for WP1/WP3 (and the JIT's type specialization):** make typed
values runtime-sound by inserting a checked coercion where `any` flows into a
typed slot — e.g. an `OpCheckType T` emitted at the `any→T` boundary, after which
downstream code may trust the type. This is a **semantic change** (`i =
someAnyString` becomes a runtime error instead of silently concatenating).

### Soundness foundation — `OpCheckType` landed

`OpCheckType` (vm/opcode.go) asserts a stack value's runtime primitive type
(int/float/str/bool) and errors on mismatch. The **compiler** (not the checker —
so every compile path, including bare `CompileWith`, is covered) tracks a
conservative static type per local *slot* (`styp` lattice in compiler.go) and
emits `OpCheckType` wherever a value that is not statically that type is bound
into a typed slot — at a primitive-annotated declaration (`var n: int = anyExpr`)
or a reassignment to a tracked slot. After the assertion the slot is guaranteed to
hold its type, so reads — and the not-yet-built specialized opcodes — may trust it
without a per-use guard.

Soundness rests on three facts: slots are never reused; captured upvalues are
**copied by value** at `OpNewClosure` (no shared boxing, so a closure can't mutate
a tracked slot behind the compiler's back); and params/upvalues/globals/dynamic
inits stay `sUnknown` (never trusted), so nothing derived from an unchecked source
is specialized. The static lattice is conservative — calls, members, and indices
are `sUnknown` — so it only ever *under*-claims, never wrongly specializes.

Scope/limits: only the four primitives are checked; compound/object annotations
stay `sUnknown` (WP3's object-shape check is the next narrowing kind). Slot-based
locals only — session top-level vars run in SharedGlobals (Env) mode and are not
slot-tracked, so their *reassignments* are unchecked (declarations are checked in
both modes). Validated: full suite + spell-bytecode parity green, zero blast
radius (no existing test relied on laundering); bytecode version 3→4 (embedded
built-in spell `.bo` regenerated).

**WP1/WP3 are now unblocked.** WP1's measured *marginal* over WP2 is modest —
stripping the int tag-check from the fused/hot ops buys LoopSum −3.8%, Fib −3.3%,
LoopEq −2.0% (WP2 already banked the dispatch/push-pop part). The larger prize is
WP3 (object slots), which additionally needs a flat-instance + vtable
representation and an object-shape `OpCheckType`.

## [A6] Type-specialized opcodes emitted by the checker ("static quickening")

- **What.** Where the checker knows operand types, emit monomorphic opcodes
  directly: `OpAddInt`, `OpAddFloat`, `OpLessInt`, `OpIndexList`, etc., instead of
  the polymorphic `OpAdd` that branches on `tag` at runtime (`vm.go:366`).
- **Why.** This is *quickening done at compile time* — and unlike runtime
  quickening (Python 3.11's specializing adaptive interpreter), it needs **no
  warm-up, no guard, no de-specialization**, because the type is proven. It deletes
  the per-op `if left.tag==tagInt && right.tag==tagInt` branch — removing a branch
  from the single most executed code in arithmetic loops (`BenchmarkLoopSum`,
  `BenchmarkLoopEq`). Runtime quickening remains useful **only** for `any`/union
  sites, where you adaptively rewrite the opcode after observing the first type.
- **Impact:** Medium. **Difficulty:** 2–3.
- **Applies to:** CP, Interp, OJIT.
- **CPU.** Removes a (usually-predicted) branch *and* shrinks the handler →
  compounds with [A4]/[A5]. The eliminated branch matters most where the predictor
  is already stressed by the shared dispatch.

## [B5→A] Profile-Guided Optimization (PGO) — listed here for its effort-adjusted rank

See [B5]; it belongs in A-tier on ROI (Difficulty 1, Small–Medium impact, zero
architecture change) but is described in B-tier to keep the JIT items together.
**Do it first** — it is the cheapest double-digit-adjacent win available.

---

# B-TIER — targeted; sequence after the substrate exists

## [B0] String interning → integer symbol IDs

- **What.** Intern every identifier, member name, and map-key-able string into a
  global/session symbol table that assigns each a dense `uint32` ID. Member names
  in bytecode, `mapObj` keys, and object field/method names become integer
  comparisons instead of string comparisons. Strings carry their interned ID
  alongside their bytes (or *are* an ID into the arena).
- **Why.** "Every string comparison becomes an integer comparison" — the classic
  win you called out. After [A3], typed object access is already integer-slot-based,
  so the residual beneficiaries are: dynamic `map` keys (`mapObj` linear scan does
  string `==` today, `value.go:430`), `any`-typed member access, and the IC keys in
  [B1]. Interning also makes the [B1] shape/IC compares single-word, and shrinks
  hashing cost for the dictionary/megamorphic path.
- **Impact:** Small–Medium (Medium on map/dynamic-heavy code). **Difficulty:** 2–3.
- **Applies to:** OM, RT, CP.
- **Go-specific.** Intern at compile time into the `Chunk` (free) and at runtime
  into the arena ([A1]); compare IDs, fall back to byte compare only on host
  boundaries. Keep the table per-Session to match the arena lifetime and avoid a
  global lock.

## [B1] Inline caches for genuinely polymorphic sites

- **What.** For `any`/union/FFI member access and method calls that [A3] can't
  resolve statically, add a per-instruction-site cache: monomorphic (one
  shape→slot), polymorphic (≤4 entries, linear), megamorphic (fall back to
  dictionary). The existing `ncache` (`vm.go:50,284`) is already a monomorphic IC
  for name lookups — generalize the pattern.
- **Why.** Standard JSC/V8 technique; turns repeated dynamic lookups into a
  compare + indexed load on the hot path. But in Buzz it is a *fallback*, not the
  main object-model strategy (that's [A3]).
- **Impact:** Medium (only on dynamic-typed code). **Difficulty:** 3.
- **Applies to:** Interp, BJIT, OM.
- **Go-specific.** Store IC state in a side array indexed by instruction offset
  (parallel to `Code`), not in the immutable `Chunk.Code`, so chunks stay shareable
  across VMs/goroutines and the cache lines you mutate aren't shared with bytecode
  you read.

## [B2] Tiered execution + hot-function/loop profiling + OSR

- **What.** Counters per function and per loop back-edge (reuse the
  `cancelN`-style cheap counter, `vm.go:197`) to promote interp → baseline → optimizing.
  **On-Stack Replacement**: when a loop gets hot mid-execution, transfer the live
  interpreter frame into JIT'd code without waiting for re-entry (map interpreter
  slots → JIT registers/stack at a loop header safepoint).
- **Why.** You only profit from JIT on code that runs enough to amortize codegen;
  tiering avoids compiling cold code (and Buzz's short Sessions make over-eager
  compilation a real risk). OSR captures long-running loops that never re-enter.
- **Impact:** Large (enables the JIT to pay off in practice). **Difficulty:** 4.
- **Applies to:** RT, BJIT, OJIT.
- **Go-specific.** Promotion/codegen must happen on a Go goroutine (it allocates
  the code buffer, touches Go maps); the running JIT thread signals "I'm hot" via
  the safepoint poll and a Go-side trampoline performs the tier-up. Keep counters
  in the pointerless hot region to avoid write barriers.

## [B3] Speculative optimization + deoptimization (tier-2 only)

- **What.** Where even the checker is uncertain (`any`, union narrowing, list
  element types, "this int never overflows", "this method is monomorphic at this
  call site across the whole program"), the optimizing JIT speculates, guards
  cheaply, and **deopts** to the interpreter on guard failure by reconstructing the
  interpreter frame from a side table.
- **Why.** Lets tier-2 generate straight-line typed code for the common case
  without proving the rare case impossible. The classic HotSpot/V8 lever — but in
  Buzz its surface area is *small* precisely because static typing already removed
  most uncertainty. Build deopt anyway: it's also how you handle redefinition,
  debugging, and unexpected `any` payloads safely.
- **Impact:** Medium (narrow surface in a typed language). **Difficulty:** 5.
- **Applies to:** OJIT, RT.

## [B4] Bounds-check elimination in JIT'd indexing

- **What.** In tier-2, hoist/remove `list`/`str` bounds checks proven redundant by
  loop induction-variable range analysis (`for i in 0..n` indexing a length-`n`
  list). Interpreter already elides *instruction-fetch* bounds (`fetch_unsafe.go`);
  this is the data-access analog inside compiled code.
- **Why.** Bounds checks are a chunk of array-loop cost and a classic JIT win
  (HotSpot's range-check elimination). Only meaningful once you emit native code;
  in the Go interpreter the check is part of `indexGet` and not separately
  removable without unsafe per-op (not worth it there).
- **Impact:** Medium (array-heavy code). **Difficulty:** 4. **Applies to:** OJIT.

## [B5] PGO build (do this now — cheapest real win)

- **What.** Collect a `pprof` CPU profile from representative Buzz workloads (the
  bench suite + real magus targets), commit it as `default.pgo`, build with Go's
  profile-guided optimization (Go 1.21+).
- **Why.** Go PGO does **profile-guided inlining** and **devirtualization of
  interface calls** — both land directly on this VM's hot path: it can inline hot
  helpers into the dispatch loop and devirtualize the `Callable`/`directObj.Fn`
  interface call in `OpCall` (`vm.go:663`). Reported single-digit % gains for
  typical Go programs, often more for tight interpreter loops, for near-zero effort
  and no source change.
- **Impact:** Small–Medium. **Difficulty:** 1.
- **Applies to:** Interp, RT, CP.
- **Go-specific.** Keep the profile fresh; a stale profile can mildly pessimize.
  Combine with [A4]: PGO uses the profile to lay out the `noinline` cold helpers
  away from the hot loop, which is the layout control Go otherwise denies you.
- **Status: DONE (WP0).** See below.

### [B5] WP0 — landed

`magus/gopherbuzz/default.pgo` is the canonical profile, regenerated by the
`regen-pgo` target in `magus/gopherbuzz/magusfile.bzz` (`magus run regen-pgo
gopherbuzz` — collects a CPU profile from the interpreter-hot bench suite). `magus/cmd/magus/default.pgo` is a **symlink** to it, so a default
`go build ./magus/cmd/magus` (`-pgo=auto`) auto-applies it to the binary and its
transitive deps, including the VM.

Mechanism note (the roadmap's "Go auto-detects `magus/gopherbuzz/default.pgo`" is only
half-true): `-pgo=auto` selects `default.pgo` **only from main-package dirs**, never
a library's. Hence the symlink in `magus/cmd/magus/`. `go test` likewise does not
auto-detect it, so benchmark gating passes `-pgo=./default.pgo` explicitly.

Measured (benchstat n=10, benchtime=300ms, `-pgo=off` vs `-pgo=./default.pgo`):

| bench | Δ | p |
|---|---|---|
| Call | **−22.4%** | 0.000 |
| LoopSum | −11.1% | 0.002 |
| FieldAccess | −8.9% | 0.000 |
| LoopSumShared | −6.8% | 0.000 |
| StringInterp | −5.3% | 0.000 |
| MethodCall | −5.3% | 0.000 |
| Fib | ~ (ns) | 0.165 |
| ForeachMap | ~ (ns) | 0.218 |
| LoopEq | **+2.4%** (regression) | 0.000 |

The −22% on `Call` is the `Callable`/`directObj.Fn` devirtualization in `OpCall`.
`LoopEq` regresses +2.4% — PGO is a global re-layout, so a minority site can get a
worse code placement; the aggregate is strongly net-positive and we keep it.
Allocs unchanged across the board. Regenerate after material dispatch/opcode
changes; a stale profile drifts toward neutral.

## [B6] SIMD for bulk operations (Go assembly)

- **What.** Vectorize *batch* operations, not the scalar dispatch loop: list
  `map`/`filter`/`sum`/`reduce` over typed numeric lists, string compare/hash, the
  `OpBuildStr` byte copy (`vm.go:956`), and GC/arena bulk scans. Implement in
  Go-Plan9 assembly (`.s`) or via an `avo`-generated kernel, gated behind a build
  tag with a portable fallback (the repo already uses this pattern — see the
  `value_unsafe`/`value_safe` and `fetch_unsafe`/`fetch_safe` split).
- **Why.** AVX2/AVX-512/NEON give 4–16× on the *vectorizable* slice of the
  workload. It does nothing for scalar interpretation, so scope it to stdlib bulk
  ops and string handling where data is contiguous (and 8-byte NaN-boxed values
  from [A1] pack cleanly into vector lanes).
- **Impact:** Medium (workload-dependent; Large for numeric-array code).
  **Difficulty:** 3–4.
- **Applies to:** RT, stdlib, OJIT.
- **Go-specific.** Go has **no SIMD intrinsics**; you must write Plan9 assembly or
  use a codegen lib. Runtime CPU-feature detection (`golang.org/x/sys/cpu`) to pick
  AVX-512 vs AVX2 vs NEON vs scalar. Assembly functions don't get inlined and have
  call overhead — only worth it past a length threshold; measure the crossover.

## [B7] Generated interpreter / opcode definitions ("mini-Deegen")

- **What.** Define each opcode's semantics once in a table/DSL and **code-generate**
  the Go interpreter `switch`, the fused superinstructions ([A5]), the
  type-specialized variants ([A6]), *and* the copy-and-patch stencil sources
  ([S1]) from that single source of truth (`go:generate`).
- **Why.** This is the Deegen thesis applied to a Go target: it keeps the
  interpreter, the specialized/fused opcodes, and the JIT stencils **in sync**, so
  the combinatorial explosion of `Op{Add,Sub,...}×{Int,Float,...}×{fused forms}`
  stays maintainable. Without it, [A5]+[A6]+[S1] become an unmaintainable hand-copy
  nightmare. It also lets you regenerate the whole opcode set when you add the
  register ISA ([A2]).
- **Impact:** Small directly, but it *unlocks* the A/S items at scale.
  **Difficulty:** 4.
- **Applies to:** CP, Interp, BJIT, OJIT.

## [B8] Compile two `Exec` variants: debug vs. release

- **What.** The dispatch loop runs `if vm.stepHook != nil && vm.stepMask&...`
  *every instruction* (`vm.go:257`). Generate/compile a `execFast` with the step
  hook code entirely absent and dispatch to it when no debugger is attached;
  `execDebug` keeps the hooks.
- **Why.** Removes a per-instruction load+branch from the hot loop for the 99.9%
  case where no `pry()` session is stepping. Small but it's in the single hottest
  loop, and it's the kind of template-specialization the brief asks for.
- **Impact:** Small. **Difficulty:** 2 (or free if generated via [B7]).
- **Applies to:** Interp.
- **CPU.** One fewer branch per dispatch at the most branch-pressured site.

---

# C-TIER — smaller / situational (do only with benchstat evidence)

- **[C1] Bytecode encoding shrink.** `Instr` is 12 bytes (`chunk.go:4`); the
  bytecode stream is *data*, so a denser encoding (e.g. `{Op uint8, A uint32, B
  uint16}` or variable-length) improves the bytecode D-cache footprint of large
  functions. Tradeoff: decode cost vs. the current single-load word-coded fetch.
  Likely neutral on small functions; measure on big ones. Impact: Tiny–Small.
  Diff: 2. (Mostly mooted by the register ISA rewrite [A2].)

- **[C2] Const-pool & string interning improvements.** `AddConst` dedups strings
  by linear scan (`chunk.go:88`) — fine at compile time. At runtime, intern strings
  into the arena and compare by offset where Buzz semantics allow, shrinking
  `OpAdd` string-concat and map-key paths. Impact: Small. Diff: 2.

- **[C3] `replaceTop2` / stack-traffic micro-tuning.** Already done well
  (`vm.go:184`). Further gains here are noise relative to A/B items; **de-prioritize
  per the brief** (sub-double-digit, already near-optimal). Revisit only inside the
  register-VM rewrite, which changes the calculus entirely.

- **[C4] `defer`/`recover` on `Exec`.** One `defer recover()` per `Exec` entry
  (`vm.go:211`) — open-coded by modern Go, ~free when not triggered, and amortized
  over a whole frame-stack run. Leave it. Only revisit if fibers/`Call` re-enter
  `Exec` at very high frequency. Impact: Tiny.

- **[C6] Struct-of-arrays for hot VM structures.** Where a hot structure is
  iterated by one field at a time, splitting `[]Value` into parallel `types
  []uint8` + `values []uint64` can improve cache use and vectorization ([B6]).
  After [A1] makes `Value` a bare `uint64`, the operand stack is *already* a flat
  `[]uint64`, so SoA's payoff narrows to specific bulk structures (e.g. a
  columnar object store, or list-of-records stdlib ops). Measure before splitting;
  AoS is friendlier for the push/pop access pattern of a stack/register window.
  Impact: Tiny–Small (situational). Diff: 3.

- **[C5] Map iteration / small-map tuning.** `smallMapThreshold=8`
  (`value.go:98`) and `keyVals` pre-building are already strong. Re-tune the
  threshold per real workload with benchstat; don't guess. Impact: Tiny–Small.

---

# Technique-by-technique verdict (the brief's wishlist)

| Technique | Verdict for Buzz | Where |
|---|---|---|
| Hidden classes / shapes | **Mostly unnecessary** — static types give compile-time slots; use shapes only for `any`/union | [A3], [B1] |
| Inline caches (mono/poly/mega) | **Yes, but as fallback** for polymorphic sites only; `ncache` already is one | [B1] |
| Quickening | **Do it at compile time** (static specialization); runtime quickening only for `any` | [A6] |
| Superinstructions | **Yes**, systematize the existing ad-hoc fusion | [A5] |
| Register vs stack VM | **Switch to register** — the main interpreter lever Go leaves you | [A2] |
| NaN boxing | **Yes, with arena** — the GC-coexistence trick is the whole point | [A1] |
| Tagged pointers | Subsumed by NaN-boxing payload encoding | [A1] |
| Pointer compression | **Yes via arena offsets** (also dodges >47-bit VA issues) | [A1] |
| Arena allocation | **Yes — highest-leverage runtime change**; fits short Sessions | [A1] |
| Custom allocators | Arena bump-allocator; per-type free lists if profiling shows churn | [A1] |
| Escape-analysis avoidance | Ongoing hygiene; audit with `-gcflags=-m` (see appendix) | appendix |
| Bounds-check elimination | Interp fetch done; **data BCE in tier-2 JIT** | [B4] |
| PGO | **Yes, immediately** — cheapest real win | [B5] |
| SIMD | **Niche** — bulk stdlib/string/array ops, not dispatch | [B6] |
| Trace JIT | **No** — method+tiering fits static types better | [S2] |
| OSR | **Yes**, with tiering | [B2] |
| Speculative opt | **Narrow surface** (static types); build for `any`/FFI | [B3] |
| Deoptimization | **Yes**, required for any speculation; design IR for it early | [S3],[B3] |
| Instruction fusion | = superinstructions | [A5] |
| IR design / SSA | **Yes, tier-2**; carry deopt metadata from day one | [S3] |
| Memory layout / cache locality | Arena + flat objects + 8-byte values | [A1],[A3] |
| Branch-prediction engineering | Limited (shared dispatch); attack via fewer dispatches + fewer in-handler branches | [A2],[A5],[A6] |
| Hot-path specialization | Static specialization + hot/cold split | [A4],[A6] |
| Generated interpreters | **Yes** — keeps specialization+JIT maintainable | [B7] |
| Computed goto in Go | **Does not exist; switch is already near-optimal** for Go. Beating it requires leaving Go (asm/JIT) | see appendix |
| Code generation strategies | Copy-and-patch (tier-1), LLVM/own backend (tier-2) | [S1],[S3] |
| Assembly-level opt | Plan9 `.s` for SIMD kernels and the JIT trampoline/encoder | [B6],[S1] |
| Go compiler limitation workarounds | `noinline` for layout, unsafe for BCE, build-tag fast/safe twins, PGO for inlining/devirt | appendix |

---

# Appendix — Go-specific constraints & how to fight them

### Dispatch: there is no computed `goto`
Go compiles a dense integer `switch` (opcodes are contiguous from 0,
`opcode.go`) to a **single jump table** — one bounds check + one indirect jump,
shared by all opcodes. You **cannot**:
- replicate the dispatch per handler (each opcode getting its own BTB-tracked
  indirect branch) — the threaded-code technique that gives C interpreters their
  edge;
- tail-call from handler to handler with VM state pinned in registers (no
  guaranteed TCO, no calling-convention control);
- use a function-pointer table faster than the switch (Go indirect calls don't
  inline and cost more than the jump-table jump).

So the Go switch is already the best *dispatch* you can write in Go. Every
interpreter A/B item above wins by **reducing dispatch count or handler size**,
not by speeding up the dispatch itself. The only way to genuinely beat it is to
stop interpreting — i.e. the JIT (S1).

### GC write barriers
Every store of a pointer into a heap object triggers a write-barrier check during
GC mark phases. `vm.stack []Value` holds `unsafe.Pointer` (`value_unsafe.go:82`),
so **every `push` of a heap value is a write-barrier candidate**. The
pointerless `[]uint64` stack from [A1] removes this entirely *and* removes the
stack from the GC's scan set. This is the biggest invisible recurring cost today.

### Escape analysis
- `Value` is a value type — good; keep it that way (NaN-boxing makes it a bare
  `uint64`, even better).
- `funObj`, `frame`, and the call path are already escape-tuned (`growWindow` is
  `noinline` specifically to keep `enterFun` inlinable — `vm.go:1043`). Preserve
  these; re-audit after any change with `go build -gcflags='-m -m'`.
- `fmt.Errorf` on error paths boxes args to `any` and allocates — fine (cold), and
  [A4] moves them out of the hot handler bodies so they stop bloating the loop.
- Watch closures and `any`-typed host boundaries for unintended escapes.

### Inlining control
Go's inliner is budget-based and opaque. Levers: `//go:noinline` (push cold code
out, [A4]), keep hot functions under the inline budget (the OpCall path already
does this deliberately), and **PGO** ([B5]) to bias inlining toward profiled hot
edges and devirtualize the `Callable` interface call.

### Native-code integration (for S1/S3)
JIT code is invisible to Go's GC and async-preemption: treat it like cgo. Run on a
`LockOSThread`+fixed-stack via a trampoline, keep all JIT-touched data in the
pointerless arena ([A1]), poll safepoints at back-edges so GC/scheduler aren't
starved, and define a fixed register/ABI contract for stencils. This integration
burden is the real reason the JIT is Difficulty 5 in Go specifically — and the
reason [A1] must land first.

### Assembly inspection is not optional
Deegen exists because compilers routinely miscompile interpreters. The Go compiler
is no exception, and it gives you *less* control, so verify rather than assume:
- `go build -gcflags='-S'` and `go tool objdump` on `Exec` after every hot-path
  change — confirm the `switch` is still a jump table, the int fast paths inlined,
  the bounds checks are gone where intended, and the cold helpers ([A4]) didn't get
  pulled back into the loop.
- `go build -gcflags='-m=2'` obsessively for escape regressions (every hot-path
  heap alloc is guilty until proven innocent).
- `-d=ssa/check_bce/debug=1` to find residual bounds checks.
- Track the dispatch loop's instruction/`.text` footprint; I-cache residency of
  that one loop dominates interpreter throughput.

### Differential-testing discipline (keep it)
The `*_unsafe.go` / `*_safe.go` build-tag split (checked twin validates the fast
twin) is exactly right and should be extended to **every** new unsafe optimization
(NaN-box decode, arena offset math, register-window access). It is what lets you
"engineer the hell out of it" without flying blind.

---

# Suggested sequencing

1. **[B5] PGO** + **[A4] hot/cold split** — days, immediate floor-raise, no
   architecture risk. Establishes the benchstat baseline for everything after.
2. **[A6] static-specialized opcodes** + **[A5] superinstructions** (ideally via
   **[B7] codegen**) — compounding interpreter wins that exploit the type checker.
3. **[A3] compile-time slots/vtables** — kills the object-model overhead;
   benchmark `FieldAccess`/`MethodCall`.
4. **[A2] register bytecode** — the big interpreter restructuring; do it before the
   JIT so the JIT lowers a register ISA.
5. **[A1] NaN-box + arena + pointerless stack** — the substrate; large interp win
   *and* the JIT prerequisite.
6. **[S2] decide method+tiering**, then **[S1] copy-and-patch baseline JIT** +
   **[B2] tiering/OSR** — the LuaJIT-class step.
7. **[S3] optimizing JIT** with **[B3] deopt**, **[B4] BCE**, **[B6] SIMD kernels**
   — the last several ×.

Gate every step on `benchstat` over the existing suite (`BenchmarkFib`,
`LoopSum`, `LoopEq`, `Foreach*`, `Call`, `MethodCall`, `FieldAccess`,
`StringInterp`, `DirectCall`) plus realistic magus-target workloads. Per the
brief: keep only what shows double-digit movement on real code; discard the rest.
