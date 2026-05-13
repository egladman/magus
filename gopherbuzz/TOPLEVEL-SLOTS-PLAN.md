# Top-level slot promotion — execution plan

Detailed execution plan for closing the top-level-variable performance gap noted
in `PERFORMANCE.md` (§[A6] / `OpCheckType`, "session top-level vars run in
SharedGlobals (Env) mode and are not slot-tracked").

**Status: M1–M4 landed.**

- **M1** — `CompileOptions.PromoteTopLevel` + the `collectFuncRefs` capture
  pre-pass + the `DeclStmt` emission change (`compiler.go`); default-off, no
  bytecode-format change. Tests in `promote_test.go`.
- **M2** — `Session.SetPromoteTopLevel`, threaded through `compileShared`
  (`session.go`); enabled on the magusfile execution path in
  `interp/runtime.go:execBuzzSrc`, left off for the REPL. Tests in
  `promote_session_test.go`. The `export`-as-visibility-boundary semantics of §3
  are now in force for magusfile execution; the full buzz suite stayed green
  (nothing in-tree relied on non-exported names leaking across files/imports).
- **M3** — benchstat (n=10): `BenchmarkLoopSumShared` → `BenchmarkLoopSumPromoted`
  is **−51.04% sec/op (p=0.000)**, landing on the slot-mode `BenchmarkLoopSum`
  ceiling (~29.6 ms) and beating gopherlua's ~42 ms. `StringInterp` reaches slot
  speed too but its gap is allocation-bound (206 allocs/op, unchanged), so on this
  host the ns delta is within noise. No-regression holds by construction: every
  other benchmark and compile path uses the unchanged flag-off branch.

- **M4** — exports-only flat-import visibility + the "did you forget to `export`?"
  diagnostic. Enforced at the **checker**, not the runtime: a flat-imported
  module's non-exported top-level names (`Chunk.Private`: captured vars and
  non-exported functions) stay live in the shared Env — so the module's own
  functions keep reading them — but `compileShared` hides them from the importer's
  checker, so only `export`ed names cross the import boundary. Same-project files
  (executed directly, not via `import`) keep full mutual visibility. Referencing a
  hidden name yields `undefined: x (an imported module declares "x" but does not
  export it — add export to it)`. Tests in `import_visibility_test.go`; the full
  buzz suite stayed green, confirming no in-tree module relied on flat imports
  leaking non-exported names.

All milestones are complete. The original design follows.

## 1. Goal and measured payoff

In SharedGlobals mode a top-level `var`/`const` compiles to an **Env binding**
(`OpDefName`/`OpLoadName`/`OpStoreName`), not a stack slot. Every top-level read
and write pays the name-resolution path (a map-resolved `(env, slot)` even with
the `ncache`) instead of a direct stack index. Block-locals (`depth > 0`) and all
function bodies already use slots (`compiler.go:407`, `:810`); only top-level
declarations are stranded on the slow path.

Measured, through the exact session path magus uses (one chunk, run repeatedly):

| Workload | top-level `var` (Env) | same code inside a `fun` (slots) |
| --- | --- | --- |
| LoopSum (1e6 adds) | ~57 ms | **~29.5 ms** |
| StringInterp (100×) | ~30 ms | ~21 ms |

For comparison, gopherlua does LoopSum in ~42 ms — so the slot ceiling (~29.5 ms)
**beats gopherlua**, while the Env path loses to it. The gap is not string-building
or formatting (allocations are identical in both columns); it is purely Env-vs-slot
variable access. This is also why Buzz already beats gopherlua on `Fib` (a
recursive function — slots) but trails on the two top-level-loop benchmarks.

**Goal:** give a top-level `var`/`const` the same slot treatment block-locals and
function bodies already get, *when it is provably safe* (§4), so script and
magusfile top-level hot code reaches the slot ceiling. Secondary benefit: a
promoted var becomes slot-tracked, which **unblocks the `OpCheckType` /
type-specialized-opcode work for top-level reassignments** that `PERFORMANCE.md`
§[A6] currently excludes.

## 2. The hard constraint (why top-level is Env today)

Top-level names are in the Env because **three mechanisms read them back by name,
after the declaring chunk's frame is gone**:

| Mechanism | How it depends on Env | Citation |
| --- | --- | --- |
| Incremental session / REPL | line N+1 is a *separate chunk* run against the same `s.env`; it resolves `x` from line N via `OpLoadName` | `session.go:233-249`, `:320-346`; `driver.go:21-41`; `repl_test.go:53-69` |
| Flat imports | `import "a"` execs file `a` into the **shared** `s.env`; the importer then resolves `a`'s top-level names by name | `session.go:358-447` (esp. `:437-444`) |
| Closures / deferred functions | a `fun` defined at top level reads top-level `x` via **live** `OpLoadName`; the function is called *after* the top-level frame returns, so `x` must outlive it | `compiler.go:305-316` (the `!c.parent.useSlots` gate), `:909-936` |
| Debugger / `.globals` | `Globals()` / `UserGlobals()` enumerate `s.env.Names()`/`Slots()` by name | `debug.go:286-295`; `driver.go:58-71`; `repl.go:610-633` |

Two facts sharpen the design:

- **`export` is a no-op for visibility today.** Exported and non-exported top-level
  vars both emit `OpDefName` into `s.env` and are *equally* visible to later
  chunks; `export` only appends the name to `chunk.Exports`, a post-hoc filter for
  `Session.Exports()` (`compiler.go:420-425`, `session.go:297-312`). There is
  currently **no** semantic distinction to exploit — we have to *create* one (§3).
- **Promoting a captured var would silently change semantics.** In slot mode a
  closure captures a top-level local as a **by-value upvalue snapshot** at
  `OpNewClosure` (`vm.go:757-778`), whereas the Env path is **by-reference / live**
  (`slot_test.go:TestSlotTopLevelClosureCapture` pins this difference). So captured
  vars must be *excluded* from promotion, not migrated to upvalues — see §4.

## 3. The semantics decision (needs sign-off before coding)

**Decision required:** make `export` the cross-chunk / cross-module visibility
boundary. A **non-exported** top-level `var`/`const` becomes *chunk-private* and
therefore slot-eligible; exported ones stay in the Env. This is conventional module
semantics (ES-module `export`, Rust `pub`) and gives `export` a real meaning it
lacks today.

Observable consequence: a flat-importing file (`session.go:437-444`) would no
longer see another file's **non-exported** top-level vars — only its `export`ed
names and its functions. This is almost certainly the intended contract (that is
what `export` is *for*), but it is a behavior change and must be signed off.
Mitigation: have the checker emit a clear "undefined variable — did you forget to
`export` it?" when a cross-file reference resolves only to a now-private name.

**Scope guard:** functions stay in the Env (they are the cross-chunk callable
surface — targets, imported helpers). Only data `var`/`const` is in scope for
promotion. This keeps the change small and avoids touching dispatch.

## 4. Promotion eligibility predicate

A top-level declaration is promoted to a slot **iff all** hold (conservative —
when in doubt, stay Env):

1. **Compile context opts in** — the magusfile/script path, *not* the REPL/
   incremental path (§6). The REPL never promotes.
2. **`depth == 0`** and it is a `var`/`const` (not a `fun`/object/enum decl).
3. **Not `export`ed** (§3).
4. **Not captured by any nested function/closure** in the same chunk — excludes
   the live-vs-snapshot hazard *and* the frame-lifetime hazard (§2) in one rule.

Rule 4 needs a **forward pre-pass**, because a var declared early can be captured
by a `fun` declared later in the same chunk. The pass walks the chunk's top-level
statement list once and collects the *must-stay-Env* set:

```
mustStayEnv = { all exported top-level names }
            ∪ { every identifier referenced inside any top-level FunDecl body
                that resolves to a top-level name (i.e. would be a capture) }
```

A top-level `var x` is promoted iff `x ∉ mustStayEnv`. The pre-pass reuses the
existing name-resolution shape (`resolveLocal` semantics) restricted to the
top-level scope; it does not need full type info.

Why this is low-risk: by construction every *promoted* var is referenced **only**
at `depth == 0` within its own chunk and never escapes via closure, import, or a
later chunk. Its lifetime is exactly the chunk's `Run`, identical to a function
local. We therefore **never** alter upvalue capture, the `resolveUpvalue` gate, or
the by-value snapshot rule — captured vars are simply left on today's Env path.

## 5. Compiler changes (concrete)

1. **`CompileOptions`** (`compiler.go:9-25`): add `PromoteTopLevel bool`, distinct
   from `SharedGlobals`. Promotion only applies when `SharedGlobals && PromoteTopLevel`.
   (REPL sets it false; magusfile path sets it true — §6.)
2. **Pre-pass** (new, in `CompileWith` before the statement loop at
   `compiler.go:44`): build `mustStayEnv` per §4; store on the compiler as a
   `promotable map[string]bool` (or a `set` of names to keep in Env).
3. **`DeclStmt` at depth 0** (`compiler.go:407-426`): change the predicate from
   `if c.useSlots || c.depth > 0` to also promote when
   `PromoteTopLevel && name promotable` → `defineLocal` + `OpSetLocal`; otherwise
   the existing `OpDefName` path (unchanged for exported/captured/REPL).
4. **Identifier reads** (`compiler.go:909-936`): no change needed.
   `resolveLocal` already returns the slot for a promoted top-level var
   (`→ OpGetLocal`); a reference from inside a nested function is, by rule 4, only
   possible for a *non-promoted* var, which still resolves via `OpLoadName`. The
   two paths stay consistent for free.
5. **Assignment** (`compileAssign`, the sole `OpStoreName` emitter): a promoted
   var resolves to a slot → `OpSetLocal`; non-promoted → `OpStoreName` as today.
6. **`LocalCount`** (`compiler.go:50-53`): already `= nextSlot`; promoted vars
   increment `nextSlot`, so the register-window pre-allocation already covers them.
   No VM change, **no bytecode-format change, no opcode change** — chunks stay
   version-compatible.

## 6. The REPL / incremental carve-out

The REPL compiles each line through `compileShared` →
`CompileOptions{SharedGlobals: true}` (`session.go:345`, `driver.go:21-41`). It
must keep seeing prior lines' vars, so it **does not set `PromoteTopLevel`** —
every REPL top-level var stays an Env binding exactly as today. Promotion is opted
in only by the whole-file magusfile/script execution path (a single chunk whose
top-level vars do not need to survive into a *later* chunk, except via the
`export`/function surface we already exclude). `session.go:Exec` gains a variant
(or a session flag) that requests promotion for the magusfile entrypoint while the
REPL path leaves it off.

## 7. Introspection / debugger

A promoted var is now a frame local. With `DebugLines` on, `defineLocal` already
records it in `LocalNames` (`compiler.go:271-284`), so the debugger shows it among
the **current frame's locals** rather than `.globals`. That is arguably more
correct (it *is* local now), but it is a visible change to `.globals` output for
magusfile debugging; call it out in the pry docs. REPL introspection is unaffected
(REPL never promotes, §6).

## 8. Risks & differential testing

- **Behavioral:** a magusfile relying on a non-exported top-level var leaking to a
  flat-importing file breaks (§3). Gate with the checker diagnostic; cover with a
  conformance case.
- **Soundness bonus, not free:** promotion makes these vars slot-tracked, so the
  `styp`/`OpCheckType` machinery now applies to their reassignments
  (`PERFORMANCE.md` §[A6]). Verify no spurious `OpCheckType` insertions and that
  the conservative lattice still only ever under-claims.
- **Differential testing (keep the repo discipline):** extend
  `slot_test.go:TestSlotEnvEquivalence` to assert promoted-mode and Env-mode
  produce identical results for every conformance program that has no cross-chunk /
  capture / export dependence; run the full `conformance_test.go` + spell-bytecode
  parity under both `buzz_safe`/`buzz_unsafe` build tags.

## 9. Milestones

- **M1 — promotion behind a default-off flag.** Pre-pass + `PromoteTopLevel`
  plumbing + `DeclStmt`/assign emission. Full suite green with the flag off (zero
  blast radius) and with a unit test flipping it on for promotable programs.
- **M2 — wire the magusfile path.** Enable `PromoteTopLevel` for whole-file
  execution; REPL stays off. Conformance + REPL cross-line + import-visibility
  tests green. Land the checker "did you forget to `export`?" diagnostic.
- **M3 — benchstat validation gate (§10).** Prove the win and the absence of
  regressions before flipping the default.
- **M4 (optional) — tighten import visibility** to exports-only (§3) if M2 left
  flat imports permissive for back-compat.

## 10. Acceptance criteria / validation

Run the `go-ultra-optimize` gate + `benchstat` (n ≥ 10, `p < 0.05`), per
`PERFORMANCE.md`'s "do only with benchstat evidence" / differential-testing rules:

- **Win:** `BenchmarkLoopSumShared` approaches `BenchmarkLoopSum` (the slot
  ceiling); the cross-engine `BenchmarkEngines/LoopSum/buzz` and
  `.../StringInterp/buzz` improve materially toward the function-body numbers in §1.
- **No regression:** `Fib`, `Call`, `ForeachList`, `ForeachMap`, `LoopEq`,
  `FieldAccess`, `MethodCall`, `Parse`, `Compile` show no statistically significant
  slowdown (the pre-pass adds compile-time work only — confirm `Compile` is flat).
- **Correctness:** full conformance + slot/env equivalence + REPL + import tests
  green under both build tags; bytecode version unchanged (no format change).

## 11. Rollback / safety

The entire change is gated by one `CompileOptions.PromoteTopLevel` flag, default
**off**. Promotion is purely additive to the compiler (same opcodes, same chunk
format), so disabling the flag — or reverting the `session.go` wiring from M2 —
restores today's behavior exactly, with no migration and no persisted artifacts to
unwind.
