---
title: Charms
description: Learn how magus charms replace one-off tool flags with shared, composable intent modifiers that patch a target's argument vector at run time.
tags: [charms, targets, argv, json-patch, rfc-6902, modifiers, cli]
---

# Charms

A **charm** is a named modifier that changes _how_ a target runs, not _which_ target or project. Where a Target answers "what operation, on what project" (see [targets.md](targets.md)) and a Spell answers "how a tool performs an operation" (see [spells.md](spells.md)), a charm answers **"in what manner."**

Charms replace the one-off boolean flags (`--write`, `--fix`, `--verbose`) that build tools accumulate. A charm is a **shared, reusable intent** applied as an **RFC 6902 JSON Patch** over the target's argument vector. There is no bespoke per-charm DSL.

## Design intent

- **Shared.** A charm name means the same thing everywhere: `rw` means "mutate in place" for `format`, `generate`, or `lint`. A charm useful only with one target is a smell; that is a one-off tool flag, not a charm.
- **Intent over implementation.** A charm says _what_ you want; each target maps it to its own tool invocation (`gofmt -w`, `prettier --write`, `golangci-lint --fix`). You never memorize per-tool flags.
- **Composable.** Charms stack: orthogonal intents like `rw` and `debug` combine without special-casing.
- **Bounded.** A charm edits arguments only; it cannot swap the command or replace the whole argv. See [Charm vs Target](#charm-vs-target-the-command-boundary).

## Additive by design

Charms only ever _add_ intent. You turn one on by naming it (`:rw`); the active set
is the union of what you named and the workspace `default_charms`, and nothing else.
There is no per-charm "off" switch, and that absence is deliberate.

The one subtraction magus offers is coarse: `--no-default-charms` drops the whole
default baseline at once, after which you re-add exactly what you want. There is no
`--without-charm=rw` to peel a single charm off the set.

Some task runners take the other path, with named configuration profiles you toggle
on and off per invocation. That turns the active configuration into a set you are
always managing: enable a default, then remember to disable it here and re-enable it
there. magus keeps the active set an explicit, additive union instead - what you see
named is what is on. If you do not want a charm's effect, do not add it.

This is a design decision, not a shortcoming. An additive-only model keeps a charm's
meaning stable and the active set legible at a glance; a subtractive one trades both
away for a convenience that usually just papers over an over-broad default. When the
defaults feel like something you fight, the fix is a smaller default set, not a
subtraction operator.

## The mechanism: a JSON Patch over the argv

A target runs a command: a `cmd` (e.g. `go`) and a base argv (e.g. `["tool", "golangci-lint", "run", "./..."]`). magus treats the argv as a JSON array and a charm as an **RFC 6902 JSON Patch** applied over it.

Each operation is `{ op, path, value?, from? }`:

| `op`      | Effect on the argv                                                | Needs   |
| --------- | ----------------------------------------------------------------- | ------- |
| `add`     | insert `value` at `path`                                          | `value` |
| `remove`  | delete the element at `path`                                      |         |
| `replace` | overwrite the element at `path` with `value`                      | `value` |
| `move`    | delete at `from`, insert at `path`                                | `from`  |
| `copy`    | read at `from`, insert at `path`                                  | `from`  |
| `test`    | assert the element at `path` equals `value` (else the run errors) | `value` |

`path` and `from` are **JSON Pointers** (RFC 6901): `"/0"`, `"/1"`, ... for a specific index; `"/-"` for append (valid only as target of `add`/`move`/`copy`).

Most authors use the [`charm` constructors](#the-charm-constructor-reference) instead of writing raw patches: they resolve a _value anchor_ to an index at author time so you never count indices.

## Applying charms on the CLI

Charms attach to a target name after `:`, comma-separated:

```sh
magus run format:rw                 # the `rw` charm on `format`
magus run lint:rw,debug api         # two charms, on `lint`, in project api
magus run go::go-test:debug         # spell-qualified op + a charm
```

The project is a **positional** argument, not part of the token. See the full grammar in [targets.md#cli-grammar](targets.md#cli-grammar).

## The `rw` charm

`rw` (read→write) is a **built-in** charm. It flips a target from check-only (read) to mutate-in-place:

| Target     | read (default)   | `rw`                                                          |
| ---------- | ---------------- | ------------------------------------------------------------- |
| `format`   | check formatting | rewrite files (`gofmt -w`, `prettier --write`, `ruff format`) |
| `generate` |                  | write generated outputs                                       |
| `lint`     | report findings  | apply autofixes where supported (e.g. `golangci-lint --fix`)  |

`rw` carries no special flag. Like every other charm, you activate it with a `:rw` suffix (`magus run format:rw`). There is no `-w`/`--write` shortcut and no `--write` flag: the suffix is the one way to ask for it.

**CI is always read-only.** `Magus.RunCI` strips the `rw` charm before dispatch, so the composite `ci` pipeline can never mutate the tree even if a caller requests it (e.g. `ci:rw`). `rw` is the only charm with this strip status; the other built-in (`cd`) and every workspace charm you define are ordinary vocabulary that survive into `ci`.

## Defaulting charms per workspace (`default_charms`)

Every run is read-only by default. A workspace can opt into a different baseline with `default_charms` in `magus.yaml`: charms applied to every `magus run` and `magus x` automatically, so a team that wants local autofix does not type `:rw` each time:

```yaml
# magus.yaml
default_charms: [rw] # `magus run format` now writes; no :rw needed
```

Per-run charms stack on top, exactly as if you had typed the whole set. Three things keep it safe:

- **`magus affected` does not apply them**, so CI (which runs `magus affected ci`) stays read-only regardless of the workspace default.
- **`RunCI` still strips `rw`**, so even a local `magus run ci` verifies without writing.
- **`--no-default-charms`** ignores the defaults for one run (`magus run format --no-default-charms` to check without rewriting).

`MAGUS_DEFAULT_CHARMS` (comma-separated) is the environment equivalent. It only sets the default baseline; it never changes what a charm means, so `has_charm("rw")` in spells and targets is unaffected.

## Stacking and composition

Charms are an **unordered, additive set**. When you pass several, they all apply:

```sh
magus run lint:rw,debug
#              │  └─ debug: add verbose flags
#              └──── rw: apply autofixes
```

The patches of all active charms are **concatenated in sorted charm-name order and applied as one sequential patch** over the base argv:

- **Deterministic.** `lint:rw,debug` and `lint:debug,rw` produce the same result; duplicates are insignificant.
- **Composable.** Charms edit individual argv elements, so edits on disjoint positions compose freely. Edits targeting the _same_ position resolve by sorted charm-name order, so one charm silently wins and the other has no effect. Because that winner is an alphabetical accident rather than a declared precedence, magus treats it as a mistake: it warns at run time and flags the overridden charm in `magus describe target ...:a,b`. Two charms that must both apply should edit different arguments, or one should own the position.

Example with base `go tool golangci-lint run ./...`:

```text
rw    : add "--fix" at /3      → go tool golangci-lint run --fix ./...
debug : add "-v"   at /-       → ... -v
```

```sh
magus run go::golangci-lint:rw,debug    # → go tool golangci-lint run --fix ./... -v
```

## Previewing the rendered command

`magus describe target` renders the **fully charm-applied command** statically (no execution):

```sh
$ magus describe target lint:rw,debug
project: .  target: lint
  charms:  [debug rw]
  spell: go
    command: go tool golangci-lint run --fix ./... -v
```

Add charms to the target ref and the `command:` line updates. Two caveats: a magusfile-function target computes its argv at runtime, so no static command is shown; and `describe` never executes or writes files even for `:rw`. (`--dry-run` reports at the target level; use `describe` to see the command itself.)

### Seeing each charm's edit: `--explain`

`--explain` turns the single `command:` line into a step-by-step trace: the base
command, then the command after each active charm's patch, in the deterministic
sorted-name order magus applies them. It is the RFC 6902 patch made legible, so
you can see exactly which charm made which edit without reading the patch data.

```sh
$ magus describe target --explain lint:rw,debug
project: .  target: lint
  charms:  [debug rw]
  spell: go
    command: go tool golangci-lint run --fix ./... -v
    charm trace:
      base       go tool golangci-lint run ./...
      + debug    go tool golangci-lint run ./... -v
      + rw       go tool golangci-lint run --fix ./... -v
```

The flag comes before the target ref (`--explain lint:rw`), like every other magus
subcommand flag. A charm that is active but changes nothing for this target adds no
line, and a charm whose patch does not apply to the command is reported as
[MGS6001](codes/charms/MGS6001.md) rather than dropped silently.

## Declaring what a charm does

A charm only does something for a target that declares it. Declarations live in a spell's `charms` table, keyed by charm name. Two charm-construction modules exist (pick by where the spell runs), plus the raw-data escape hatch.

### 1. Built-in command spells (`import "magus/charm"`)

A built-in command spell is **self-contained**: it imports only the pure-Buzz modules `magus/target` and `magus/charm`, so it compiles to bare bytecode with no host bindings. `magus/charm` exports the core constructors as **bare functions** (the same flat-import idiom `magus/target` uses for `Target`). Each resolves a _value anchor_ to an index so you never count positions:

```buzz
import "magus/target";
import "magus/charm";

fun lint(target: Target) > Command {
    final args = ["tool", "golangci-lint", "run", "./..."];
    return Command{bin = "go", args = args, charms = {
        "rw":    after(args, "run", ["--fix"]),  // insert after "run" - index-proof
        "debug": append(["-v"]),                 // append
    }};
}
```

`magus/charm` ships the **core** constructors: `append`, `prepend`, `after`, `before`, `set`, `drop`. For the advanced set (`move`/`copy`/`test`, the predicate `*Func` variants, `path`) reach the host `charm` module from a non-built-in spell (below). The constructors return the same `{ops: [...]}` record the raw form below produces, so the two interoperate.

### 2. Workspace spells & magusfiles (`import "charm"`)

A workspace spell (imported by path) or a magusfile runs with host bindings, so it imports the host **`charm`** module, which carries the **full** constructor set as namespaced methods:

```buzz
import "charm";
final args = ["tool", "golangci-lint", "run", "./..."];
charms = {
    "rw":    charm.after(args, "run", ["--fix"]),  // insert after "run"
    "debug": charm.append(["-v"]),                 // append
    "stamp": charm.move(args, "run", charm.path(args, "./...")), // advanced: see below
};
```

> The argument-removing constructor is named **`drop`** (`charm.drop`), not `remove`: a charm module is a Buzz map, and the built-in map `.remove()` method would shadow `remove`. This is a _constructor name only_. `charm.drop` emits the standard RFC 6902 `{"op": "remove", ...}` op. The patch vocabulary does not change; magus never deviates from RFC 6902.

### 3. Raw RFC 6902 data (the lowest level)

The constructors are pure convenience: they only **build the `{ops: [...]}` record**, a plain RFC 6902 JSON Patch. That record _is_ the underlying type a charm declares (`Charm{Ops: []PatchOp}`, see [the patch model](#reference-the-patch-model)). A constructor resolves the anchor to an index and returns the same record you could type by hand. The two forms are interchangeable; the helper used by the bundled spells and the literal it returns are the same value:

```buzz
final args = ["tool", "golangci-lint", "run", "./..."];

"rw": after(args, "run", ["--fix"]),                       // helper: anchors on "run"
"rw": {"ops": [{"op": "add", "path": "/3", "value": "--fix"}]},   // ≡ the record it returns
```

You can **declare the patch notation explicitly** for an op no constructor covers (several edits in one charm), for full control, or by preference. The raw form is first-class, not a fallback. What you give up by hand is the anchoring: `"/3"` is a counted index that silently breaks if an earlier arg moves, whereas `after(args, "run", ...)` recomputes it. That index-proofing is why the bundled spells prefer the helper.

The six ops are exactly RFC 6902's (`add`/`remove`/`replace`/`move`/`copy`/`test`); see [the patch model](#reference-the-patch-model). magus adds no ops and renames none; the constructors are sugar over this vocabulary.

### 4. Function targets & ops (branch in code)

When the argv needs to be computed, branch in code. A magusfile function target receives the forwarded CLI args:

```buzz
export fun lint(args: [str]) > void {
    var fix = false;
    for (a in args) { if (a == "--write") { fix = true; } }
    os.exec("golangci-lint", if (fix) ["run", "--fix"] else ["run"]);
}
```

A function target reads the active charm set directly with **`magus.has_charm(name)`**, including the built-in read→write toggle, `has_charm("rw")`. This is how a function target _selects which command to run_, the one thing a charm itself cannot do (see [the boundary](#charm-vs-target-the-command-boundary)). For example, a `build` target can compile the host binary by default and switch to the container image under a `container` charm:

```buzz
export fun build(args: [str]) > void {
    if (magus.has_charm("container")) { magus.needs(image_build); }
    else { magus.needs(go_build); }
}
```

Because the toggled targets are reached by nested dispatch (not the top-level selection), `build:container` trips the soft typo-guard warning. That is expected here, since a function target legitimately reads a charm no spell declares (see [Discovery](#discovery)).

Spell op methods receive the active charm set as `opts.charms` (a lookup table: `if opts.charms.rw then`). **Charms a spell does not declare or test for are ignored.**

## The `charm` constructor reference

Both charm modules build a charm's patch; every constructor returns `{ ops = [...] }`. The `argv`-taking constructors resolve a _value anchor_ (or predicate for the `*Func` variants) to a numeric JSON Pointer at author time, so the stored patch is pure positional RFC 6902.

The table is the **full** set, available on the host `charm` module (`import "charm"`, called `charm.after(...)`). The pure-Buzz `magus/charm` module (`import "magus/charm"`, called bare as `after(...)`) exports the **core** rows (append, prepend, after, before, set, drop) for self-contained built-in spells.

| Constructor                          | Builds                                                                                       |
| ------------------------------------ | -------------------------------------------------------------------------------------------- |
| `append(vals)`                       | add each of `vals` at the end (`/-`)                                                         |
| `prepend(vals)`                      | insert `vals` at the front, in order                                                         |
| `after(argv, anchor, vals)`          | insert `vals` just after the first element equal to `anchor`                                 |
| `before(argv, anchor, vals)`         | insert `vals` just before `anchor`                                                           |
| `set(argv, anchor, val)`             | replace the element equal to `anchor` with `val`                                             |
| `drop(argv, anchor)`                 | remove the element equal to `anchor`                                                         |
| `afterFunc(argv, fn, vals)`          | `after`, but match by predicate                                                              |
| `beforeFunc(argv, fn, vals)`         | `before`, by predicate                                                                       |
| `setFunc(argv, fn, val)`             | `set`, by predicate                                                                          |
| `dropFunc(argv, fn)`                 | `drop`, by predicate                                                                         |
| `move(argv, anchor, to)`             | move the `anchor` element to pointer `to` (`"/-"` end, `"/0"` front, or `path(...)`)         |
| `copy(argv, anchor, to)`             | copy the `anchor` element to pointer `to`                                                    |
| `test(argv, anchor)`                 | guard: assert `anchor` is still at its position when the patch applies (else the run errors) |
| `moveFunc` / `copyFunc` / `testFunc` | the above, matching by predicate                                                             |
| `path(argv, anchor)`                 | the JSON Pointer (`"/N"`) of `anchor`, for use as a `to` destination or in a hand-written op |
| `pathFunc(argv, fn)`                 | `path`, by predicate                                                                         |

Method names are camelCase (`charm.afterFunc`, `charm.pathFunc`), following Buzz's convention.

A missing anchor is a **load-time error**, not a silently wrong index.

## Use-case cookbook

The examples use the host module (`charm.*`); inside a self-contained built-in spell call the bare `magus/charm` form (`append(...)`, `after(...)`, and so on) instead.

**Append a flag** (e.g. a `debug` charm adding `-v`):

```buzz
debug = charm.append(["-v"]);
// {"ops":[{"op":"add","path":"/-","value":"-v"}]}
```

**Insert after a subcommand** (anchor by value, index-proof):

```buzz
// base ["test", "./..."]: add -race right after "test"
race = charm.after(["test", "./..."], "test", ["-race"]);
// {"ops":[{"op":"add","path":"/1","value":"-race"}]}
```

**Swap a flag** (e.g. `gofmt -l .` → `-w .`):

```buzz
rw = charm.set(["-l", "."], "-l", "-w");
// {"ops":[{"op":"replace","path":"/0","value":"-w"}]}
```

**Drop a flag** (e.g. `ruff format --check .` → `ruff format .`):

```buzz
rw = charm.drop(base, "--check");   // host; bare drop(base, "--check") in magus/charm
// {"ops":[{"op":"remove","path":"/3"}]}
```

**Several edits in one charm** (remove higher indices first to avoid reshuffling). Drop to the raw form, since each constructor yields a single op:

```buzz
// cargo fmt -- --check  →  cargo fmt
rw = { "ops": [
    {"op": "remove", "path": "/2"},   // "--check"
    {"op": "remove", "path": "/1"},   // "--"
]};
```

**Move/copy/test (advanced, host module only).** Reposition an existing arg, or guard that one is where you think:

```buzz
// move the matched flag to the end; charm.path resolves an anchor to its pointer
reorder = charm.move(base, "--config", "/-");
// assert "run" is still at its index when the patch applies, else the run errors
guard   = charm.test(base, "run");
```

**Match by predicate** (the `*Func` variants, host module only):

```buzz
cap = charm.setFunc(base, fun(s: str) > bool { return s.startsWith("-j"); }, "-j16");
```

Conditional or per-invocation logic belongs in a **function target**, not a charm. Charms are static data resolved at author time.

## Charm vs Target: the command boundary

**A charm rewrites a target's arguments. It can never change the base command (`cmd`) or replace the whole argv.** `ValidatePatch` rejects the root pointer (`""`), so every charm op edits an _element_ of the argv, never the array as a whole.

`ValidatePatch` enforces this boundary deliberately.

### Decision guide

| You want to...                                                                                    | Use a...                                          | Why                                          |
| ------------------------------------------------------------------------------------------------- | ------------------------------------------------- | -------------------------------------------- |
| run the **same command** with different flags, as a reusable named intent (`rw`, `debug`, `race`) | **charm**                                         | shared vocabulary; composes; CI can strip it |
| run a **different command**, or compute the argv, or branch on runtime state                      | **target** or **spell op**                        | only a target/op defines a command           |
| pass a flag for **one invocation only**                                                           | `--` passthrough (`magus run test -- -run TestX`) | not a reusable intent                        |
| pick **which project / spell** runs                                                               | positional project arg / `spell::` qualifier      | identity, not a modifier                     |

### Why the boundary exists

- **Composability.** Argument edits layer cleanly across charms. Two charms each replacing the whole command is an unresolvable conflict; element-level edits have a well-defined merge.
- **The one-intent contract.** A charm name is supposed to mean the same thing everywhere. A charm that redefines a target's command is a bespoke per-target redefinition wearing a charm name: `rw` would mean `gofmt -w` here, `terraform apply` there, and the name no longer carries a single meaning.
- **Transparency.** With the boundary, the running command is the target's declared `cmd` (visible in the spell, `magus describe`, logs). Without it, the real command would depend on an invisible matrix of active charms.
- **Abuse prevention.** Without the boundary, charms become a general-purpose override mechanism, and the shared vocabulary rots into hidden behavior per project.

**Charms modify args; targets define commands.** When you want a different command, write a target.

### When you've left the charm layer

**Function target** (most common): write an exported function and call the tool via `os.exec`:

```buzz
// magusfile.buzz
import "os";
export fun lint(args: [str]) > void {
    var fix = false;
    for (a in args) { if (a == "--write") { fix = true; } }
    os.exec("golangci-lint", if (fix) ["run", "--fix", "./..."] else ["run", "./..."]);
}
```

**Workspace spell**: author a `spells/<name>.buzz` spell (imported by path) with an `ops` entry and wire per-project charms there. The spell owns the _command_; charms tune its _args_.

**`::` hatch**: `magus run go::go-vet api` reaches a single spell op directly. It is an escape hatch, not the everyday surface.

## Dynamic values: no interpolation, use the language

Charm args are **literal**: there is no `${VAR}` interpolation, by design. The host language is the interpolation engine:

- **Known at load time:** build the string in code and pass it to a constructor:

  ```buzz
  import "charm";
  import "env";
  charms = { "rw": charm.after(base, "run", ["--config={env.get("LINT_CONFIG")}"]) };
  ```

- **Per-invocation:** use a function target. Charms are static data; they cannot read the env at run time.

## How a charm reaches a spell

```text
magus run lint:rw,debug
      │
      ▼
ParseTarget       → Target{Name:"lint", Charms:["rw","debug"]}
      │
      ▼
WithCharms(ctx)   → ctx carries {"rw","debug"}           types/charm.go
      │
      ▼
spell.Cast(ctx)   → HasCharm(ctx,"rw") / HasCharm(ctx,"debug")
      │
      ▼
resolveCharmArgs  → concatenate active charms' patches (sorted name),
                    apply once over the base argv                fork.go
```

`HasCharm` is a set-membership test; a spell reacts to charms it knows and ignores the rest. Charms are never part of a Target's durable identity (which is `Path + Name`; see [targets.md](targets.md)).

## Discovery

`magus describe <target>` lists the charms a target declares and renders the resulting command (see [Previewing the rendered command](#previewing-the-rendered-command)):

```sh
magus describe target lint:rw,debug
# → project: .  target: lint
#     charms:  [debug rw]
#     command: go tool golangci-lint run --fix ./... -v
```

An active charm that no selected target declares (and isn't a reserved built-in like `rw`) prints a soft warning as a typo guard. It is only a warning, not an error, because a function target may legitimately read a charm it never declares.

## Naming

Charm names use the target-name charset: letters, digits, `-`, `_` (`types.ValidateCharmName`). By convention they are lowercase and represent **shared vocabulary** across the workspace: define a charm's meaning once and honor it everywhere. A charm useful only with one target is a smell; that is a one-off tool flag (pass it after `--`).

Names are normalized the same way target names are (`types.NormalizeCharmName`, kebab-case), so matching is case- and separator-insensitive on both sides: `:Rw`, `:rw`, and `:RW` are one charm, as are `:no_cache` and `:no-cache`. A spell that tests `has_charm("noCache")` is matched by a `:no-cache` suffix and vice versa; declaration and invocation can't drift on spelling.

## What is not a charm

- **A different command.** Charms rewrite args, never `cmd`; see [the boundary](#charm-vs-target-the-command-boundary).
- **A whole-argv rewrite.** The root pointer is rejected; express the change as individual `replace`/`remove`/`add` ops.
- **Project selection** (`api`, `/`): positional arguments, not charms.
- **Spell qualifier** (`go::`): a `RunOption` (`WithSpellFilter`), stripped by the CLI before charms are parsed.
- **One-off tool flags**: pass these after `--` (`magus run test -- -run TestX`).

## Reference: the patch model

```go
type PatchOp struct {
    Op    string // add | remove | replace | move | copy | test
    Path  string // JSON Pointer: "/N" or "/-" (add/move/copy targets only)
    Value string // for add | replace | test
    From  string // for move | copy
}

type Charm struct {
    Ops []PatchOp // an RFC 6902 patch over the target's argv
}
```

`ValidatePatch` enforces: the `op` is one of six; `path` is non-empty and rooted at `/` (root path rejected, forbidding whole-argv replacement); `move`/`copy` carry a `/`-rooted `from`. `ApplyPatch` runs ops in order over a copy of the argv (the base is never mutated), with bounds-checked indices. The implementation is verified differentially against the reference RFC 6902 library (`evanphx/json-patch`).

## Glossary

| Term             | Definition                                                                                                                                                                                                                                                                        |
| ---------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| **Charm**        | A named, shared execution modifier carried in context. `Target.Charms` / `[]string`.                                                                                                                                                                                              |
| **`rw`**         | A built-in charm: mutate in place (format/generate; lint autofix where supported). Read via `has_charm("rw")`. Stripped from `ci`.                                                                                                                                                |
| **`gha`**        | A built-in charm: opt into GitHub Actions output. Swap a tool to its GHA annotation format (ruff/buf/sqlfluff/vitest), or have a target emit GHA-shaped output (the `ci-shard` job matrix → `$GITHUB_OUTPUT`). Set via `:gha`. A no-op where unsupported; not stripped from `ci`. |
| **JSON Patch**   | The RFC 6902 document a charm declares: an ordered list of element-level ops (`add`/`remove`/`replace`/`move`/`copy`/`test`) over the target's argv.                                                                                                                              |
| **PatchOp**      | One operation: `{op, path, value?, from?}`.                                                                                                                                                                                                                                       |
| **Anchor**       | A value (or predicate) a `charm.*` constructor resolves to a numeric JSON Pointer at author time.                                                                                                                                                                                 |
| **Stacking**     | Multiple charms apply together: patches concatenate in sorted-name order and apply as one sequential patch.                                                                                                                                                                       |
| **The boundary** | Charms edit argv elements only (never `cmd`, never the whole argv). Enforced by `ValidatePatch`.                                                                                                                                                                                  |
| **`HasCharm`**   | The set-membership query a spell uses to react to a charm; unknown charms are ignored.                                                                                                                                                                                            |

## See also

- [operations.md](operations.md): the Operation whose argv a charm patches, and where charms sit in the hierarchy.
- [targets.md](targets.md): what a Target is, and the CLI grammar charms attach to.
- [spells.md](spells.md): what a Spell is, and [Spells vs Targets](spells.md#spells-vs-targets).
- [modules/charm.md](buzz/modules/charm.md): the generated `charm` module API reference.
