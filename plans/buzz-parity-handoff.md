# Handoff: gopherbuzz upstream parity

Updated 2026-07-28. Branch `feat/plans-buzz-parity-handoff-9b8119`, seven
commits on top of `add-install` (`48564ef3` -> `d2424f8f`). Everything below is
COMMITTED; the tree is clean apart from this `plans/` directory. Nothing is
pushed.

Parity went **27 -> 48 of 83** this session (12 -> 27 the session before).

---

## Start here

```sh
go build -o /tmp/magus ./cmd/magus            # HEAD, per CLAUDE.md
cd libs/gopherbuzz
GOPHERBUZZ_UPSTREAM_DIR="$HOME/.cache/magus/gopherbuzz-upstream-buzz" \
  go test -run TestUpstreamConformance -count=1 .
```

`magus run conformance gopherbuzz` fetches `buzz-language/buzz` at the pinned
sha into that cache dir if it is missing.

**Always measure against the PINNED commit.** A plain `go test ./...` falls back
to `~/Repos/buzz`, which is several commits ahead; the test SKIPS on a ref
mismatch rather than reporting a meaningless comparison.

### The conformance suite is NOT the CI gate

This was the biggest correction of the session. `magusfile.buzz:122` makes
`conformance` opt-in and explicitly not part of `ci`, because it needs network
access and a foreign clone. So the allowlist protects nothing in CI, and
everything the previous session landed was ungated.

`libs/gopherbuzz/parity_test.go` is the hermetic gate: ~90 cases in strict
(upstream-parity) mode with no host wiring, one or more per feature below. It
runs in plain `go test`. **Add to it for every future parity gain** - the
allowlist records the score, `parity_test.go` is what actually stops a
regression.

It was mutation-tested, not assumed: injecting three deliberate regressions
caught two and exposed a tautological assertion plus a labeled-break case that
could not observe a stranded iterator state. Both were fixed. If you verify
tests that way again, do it in a throwaway copy - a mutated compiler left on
disk across a tool boundary is how this session nearly shipped a broken tree.

### To see WHY each red file is red

There is no committed reporter. Drop a scratch `_test.go` in
`libs/gopherbuzz` that globs the upstream behavior dir and calls
`runUpstreamBehaviorFile` (both are in `conformance_test.go`), print name +
detail per failure, then delete it. Do not commit it.

---

## Landed this session

Multi-clause `for`; nullable declarations without an initializer; object-literal
field punning; `> void` arrow bodies (including an assignment as the body);
`enum<T>` backing types and explicit case values; enum-from-value (`Suit(1)`);
optional chaining `?.`/`?[`; default argument values; `!>`/`*>` inside a
parameter's function TYPE; multiple typed `catch` clauses with rethrow;
`catch (e: any)`; labeled loops; block expressions (`from { ... out v; }`);
free identifiers (`@"..."`); generic object declarations; inline `if`
expressions; `catch void`; contextual typing threaded through call arguments,
parameter defaults, and anonymous object literals; range precedence and the
range method set; anonymous object literals resolving to their expected object;
paren-free calls (`callMe .{ ... }`).

Two checker bugs fell out of that and are worth knowing:

- An object's method signatures are built while the top level is still being
  registered, so a method annotated with a type declared LATER in the file
  resolved to nothing and was never refreshed. Call sites read that stale map.
  `checkObjectDecl` now writes the rebuilt signature back.
- `ot.Fields` holds `types.ParseAnnot` output, which knows the primitives but
  not the checker's named types. Anything matching against a field's type must
  `resolveType` it first. The field-default code already documented this trap;
  the new code hit it again.

Bytecode went 12 -> 18. **Every bump invalidates the committed spell blobs** and
the binary then panics on EVERY command with `unmarshal built-in bash.bo:
version mismatch`. Fix, do not revert:

```sh
cd internal/spell && go run ../../cmd/magus-utils spells -spells ../../spells -out gen
```

---

## The most valuable thing left: by-value upvalue capture

**Closures capture upvalues by VALUE**, a snapshot taken at closure creation. A
closure that assigns to an enclosing local updates only its own copy:

```buzz
var sum = 0;
final add = fun (n: int) > void { sum = sum + n; };
add(5);   // sum is still 0; upstream leaves it at 5
```

It answers rather than errors, which makes it worse than the compound-assign
divergence already in the README. It is what blocks `functional.buzz` (whose
`forEach` tests accumulate into an enclosing variable) and it is a live
correctness bug for anyone writing Buzz today, not just a parity gap.

Fixing it means boxing upvalues into cells (open upvalues aliasing the stack,
closed on scope exit - the Lua design) rather than copying Values at
`OpNewClosure`. That is a VM change, so re-read README gotcha #1 first: a new
`case Op...:` in the Exec switch regresses ALL benchmarks 25-55%. Benchmark
`LoopSum`/`LoopSumPromoted` before and after; they sat at ~2.9 ms and 4
allocs/op across every change this session.

Two tests in `parity_test.go` pin the current behaviour so the fix has to be
deliberate: `TestParity_ClosureUpvaluesAreCapturedByValue` and
`TestParity_VoidArrowBodyAcceptsAnAssignment`. Both will need updating - that is
the point.

---

## What is left, measured

| Gap | Files blocked | Notes |
| --- | ---: | --- |
| Type-value literals `<T>` + `typeof` | 5 | lists, maps, clone-mutability-methods, inferred-enum-case, types-as-value |
| `match` expressions | 2 | match, forward-method-placeholders |
| Forward-referenced top-level placeholders | 2 | control-flow, common-namespace |
| Selective/aliased imports | 2 | import-lib, import-export |
| Tuples (`.{ a, b }` positional, `.0`) | 1 | tuples |
| By-reference upvalues | 1 | functional (see above) |
| Anonymous object TYPES in the checker | 1 | anonymous-objects: `_: obj{...} = x;` |
| `as`-binding in an if condition | 1 | any: `if (x as name: str)` |
| Nested backtick interpolation | 1 | multiline-strings |
| Map arithmetic | 1 | composite-assign |
| `protocol` declarations | 1 | protocols |
| `pat.matchAgainst`/`matchAllAgainst` | 1 | pattern; returns match objects with `.capture`, not `[str]` |
| Buffer std object | 1 | buffer |
| `buzz:test` module | 1 | testing |
| Mutual imports | 1 | mutual-import |

**`<T>` is the biggest single item but is NOT cheap.** `typeof list == <[any]>`
and `typeof slist == <[str]>` require list and map VALUES to carry their element
types, which they do not today - that is a runtime representation change, not a
parser one. `types-as-value.buzz` additionally needs `protocol` and zdef
fstructs, so closing `<T>` flips four files, not five.

### Deliberate non-goals (documented in the README, do not re-litigate)

- **`math\deg`** - upstream's expected value implies 57.295779513082195; the
  correctly-rounded f64 is 57.29577951308232, which is what gopherbuzz returns.
- **GC collector callbacks** - Go's GC gives no guarantee at a program-controlled
  point.
- **`std\toUd`** - userdata is Zig-specific.
- **Environment-dependent**: `os.buzz`, `run-file.buzz`, `fs.buzz` need
  upstream's own binary or a cwd at upstream's repo root (`fs.buzz` asserts
  `README.md` is listable and moves it into `src/`). `fs\deleteDirectory` is
  genuinely missing, but adding it does not make the file pass.
- **FFI**: `c-buzz-api`, `extern-library`, `ffi` need a compiled native lib.

---

## Traps, still current

1. **Bytecode bump invalidates the spell blobs** - see the regen command above.
2. **No new `case Op...:` bodies in the Exec switch** (README gotcha #1).
   Everything this session added reused existing opcodes: optional chaining is
   built from `OpJumpIfNull` + `OpJump`, and enum-from-value went into the
   existing `OpCall` case.
3. **Adding a method to the magus module** breaks `TestModuleDocsUpToDate` (run
   `go run ../cmd/magus-docs -out ./reference/buzz` from `docs/`) and
   `TestMagusSurfaceMatchesBindings`, which needs a matching stub in
   `internal/dry/host.go`.
4. **Reserved words bite in fixtures.** `map`, `static`, `fib`, `test`, `double`,
   `type`, `out`, `from` are all reserved and fail with a confusing error. Three
   false leads last session came from fixture syntax, and one this session
   (`fun double(...)`).
5. **Buzz object literals use `=`, not `:`** (`Point{ x = 1 }`).
6. **`readType` reconstructs a type's text from its tokens.** Anything skipType
   consumes that is NOT part of the type's identity must be recorded in
   `p.typeTextSkips` or it lands in the annotation string and corrupts it. Both
   the `!>`/`*>` suffixes and `::<...>` instantiations use this.

---

## Open, not started

- **`TopoSort`'s doc comment is wrong.** [types/graph.go](../types/graph.go) says
  it returns "dependencies before dependents"; it returns the reverse. Left
  alone because callers may depend on the behaviour.
- **`magus\cmd` is still present.** `ls`, `affected`, `where` and `graph` are
  covered in-process; `describe*`, `query`, `explain`, `path`, `refs`,
  `insight`, `status`, `clean`, `version`, `tail`, `watch` are not.
- **The checker types no host-module member.** `magus\nosuchmethod()` passes and
  dies at runtime. This is why `io.buzz` and `crypto.buzz` still fail: an
  inferred enum case in a labeled argument to a HOST function has no signature
  to resolve against. Fixing host-module typing would close both.
- **`TestProductionCodeUsesSharedCodec` fails** on files under
  `.claude/worktrees/` only. Clear dead worktrees to settle it.
