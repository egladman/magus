# Plan: typed target returns and chainable resource types

Written 2026-07-29, overnight, on `add-install`. Prompted by: "one thing I really
liked about Dagger is that everything was typed, and you could choose to return
files and file paths and file contents in stdout all seamlessly from the CLI."

Status: DESIGN. Nothing here is implemented. Research is grounded in
docs.dagger.io (cited inline) and in this repo's code (file:line).

## The one-line finding

`internal/interp/runtime.go:293` is `_, err = fn(ctx, buzzArgs)`.

A target's return value is discarded at exactly one line. The dispatcher built at
`runtime.go:475` already returns `(vm.Value, error)`, and `CallValue` already
carries the value back. Targets are `> void` by CONVENTION, not by mechanism.

This is the whole reason the feature is worth doing: it is not an architecture
change, it is a seam that was left unplugged.

## What Dagger actually does

Verified against docs.dagger.io/api/{types,chaining,arguments,custom-functions}.

1. A module function's Go signature is reflected into a schema. The CLI is
   GENERATED from that schema - subcommands, flags, and help text all derive from
   types, so the CLI can never drift from the code.
2. Parameters become flags: `name string` -> `--name`. Optionality and defaults
   are pragma comments above the parameter (`// +optional`, `// +default="world"`).
3. Return types are objects with their own methods, so calls CHAIN:

   ```sh
   dagger call build --source=. file ./dagger export --path=./my-file
   ```

4. A handful of CORE types carry universal CLI verbs:
   - `File` -> `contents`, `export --path=`, `size`
   - `Directory` -> `entries`, `export --path=`, `export --wipe`
   - `Container` -> `with-exec`, `stdout`, `publish`, `as-service`, `terminal`
   - `Service` -> `up`, `up --ports=`
   - also `Secret`, `CacheVolume`, `GitRepository`, `Socket`, `Port`
5. Those same types work as INPUTS: `--source=/path/README.md` or
   `--source=https://github.com/dagger/dagger.git#main:README.md`. The CLI
   converts either into a `File` object.

The property worth stealing is #3 plus #4: **a typed return value is not a
string the CLI prints, it is an object the CLI knows verbs for.**

## What magus already has (more than expected)

| Dagger concept | magus today | gap |
| --- | --- | --- |
| typed function signature | `fun build(ctx: magus\Context, args: [str]) > void` | return is always void |
| return value plumbing | `targetMap` returns `(vm.Value, error)` | discarded at runtime.go:293 |
| value-type mirrors | 15 generated `object` mirrors in `magus/target` | none describe a File/Dir |
| a target's produced files | `ctx.outputs("dist/*")`, declared for CACHING | not exposed as a value |
| CLI output shaping | `-o json` / `-o name` / `-o template=` | applies to magus's own verbs, not to a target result |
| file classification | `magus describe file` -> role/project/owner | not reachable from a target result |

The fourth row is the important one, and it is where magus should NOT copy
Dagger.

**Dagger makes you return a File. magus already knows what files a target
produces, because it declared them for the cache.** `ctx.outputs("dist/*")` is a
Directory in all but name. Every target in this repo already carries that
declaration. So the magus-native form of the feature needs no new declaration
from the user at all - the data has been sitting there powering `magus clean
--outputs` and the merge driver the whole time.

That is a genuinely better story than Dagger's, and it is worth leading with.

## Design

### Phase 1 - a target may return a value

Stop discarding it. `runtime.go:293` keeps the value; `magus run` renders it
through the output arm that already exists (`ResolveOutput` / `writeFormatted`).

```buzz
export fun version(ctx: magus\Context, args: [str]) > str {
    return vcs\describe();
}
```

```sh
magus run version                 # 0.4.1
magus run version -o json         # {"value":"0.4.1"}
```

Rules:

- `> void` stays the default and prints nothing. Every existing target is
  unaffected; this is purely additive.
- A returned value must be a mirrored boundary type or a scalar. That constraint
  is already enforceable: the 15 mirrors are the closed set of things that cross
  the boundary, and `TestEveryToMapOwnerIsMirrored` keeps it closed.
- Caching: a target's return value must be part of what the cache stores and
  replays, or a cache hit would print nothing on the second run. This is the
  main piece of real work in Phase 1, and it is the same shape as the existing
  captured-output replay.

### Phase 2 - `File` and `Directory` as boundary types

Two new mirrors, generated the same way as the other 15:

```buzz
export object File { path: str = "", project: str = "", role: str = "" }
export object Directory { path: str = "", project: str = "", entries: [str] = [] }
```

`role` is not decoration. It is what `magus describe file` already computes
(`output` / `source` / `unclaimed`), so a returned File says whether it is
generated - the fact an agent most often gets wrong.

CLI verbs, chained after the target:

```sh
magus run build file --path dist/magus contents
magus run build file --path dist/magus export --path ./out/magus
magus run build outputs entries
magus run build outputs export --path ./dist-copy
```

`outputs` is the free one: it needs no return statement, because the target
already declared `ctx.outputs(...)`. `magus run build outputs` is answerable
today from `Project.AllOutputs()` without running anything new.

### Phase 3 - typed parameters instead of `args: [str]`

Today every target takes `args: [str]` and parses positionally. Dagger's
parameters-become-flags is the better model, and `extern fun`
(landed this session, `c58c49f9`) plus the annotation checker make it expressible:

```buzz
export fun release(ctx: magus\Context, version: str, dryRun: bool = false) > File
```

```sh
magus run release --version 1.2.0 --dry-run
```

This is the largest phase and the most disruptive; it changes the target
contract that `ctxForm` validates at `runtime.go:468`. It should not start until
Phases 1 and 2 have shipped and been used.

## Why this matters for agents

From the thesis (`why-magus-exists-thesis`): the product is the diagnostics and
the DX, not the task running. This feature is squarely that.

An agent that runs a build today has to GUESS where the artifact went, then guess
whether the file it found is generated or hand-written. Both guesses are wrong
often enough to matter, and both are already answered inside magus. A typed
return turns two guesses into two commands:

```sh
magus run build outputs -o json     # exactly what was produced
magus describe file <path>          # what it is
```

It also composes with the guard: a `File` carrying `role=output` is the same
classification `magus agent hook --path` uses to advise against hand-editing.

## Risks and open questions

1. **Cache replay is the hard part.** A returned value must survive a cache hit.
   If it does not, `magus run version` prints on the first run and nothing on the
   second, which is worse than not having the feature. Design this before writing
   Phase 1, not during.
2. **Chained subcommands vs the existing CLI grammar.** `magus run <target>
   [flags] [project...]` already takes trailing project args. `magus run build
   file --path x contents` has to be unambiguous against `magus run build web
   api`. This needs a real grammar decision, possibly a separator. Memory says
   "minimize CLI surface, fold don't add", so a new top-level verb is the wrong
   answer; folding into `run` needs care.
3. **Do NOT copy Container/Service.** magus is not a container runtime, and
   `Secret` overlaps the existing config surface. The valuable subset is `File`
   and `Directory` only. Copying the rest would be cargo-culting Dagger's
   architecture rather than its idea.
4. **Scope creep into a DSL.** Phase 3 edges toward re-inventing a function-call
   CLI. The stopping point should be explicit: typed params, no chaining ON
   params.

## Sequencing

Phase 2's `outputs` verb is the cheapest thing with the highest payoff, and it
does not depend on Phase 1: it reads declarations, runs nothing, and needs no
cache work. **Start there**, not at Phase 1. It proves the pattern with almost no
risk and immediately helps agents.

Then Phase 1 (typed returns + cache replay), then reassess Phase 3.

## Progress, 2026-07-29

**Done (`210995cb`).** The declaration half of Phase 2 landed without any new CLI
surface at all. `magus describe target` was reading `baseStep`, the project-wide
globs, so a target whose outputs come from `ctx.outputs` described itself as
producing nothing: `magus describe target md-generate` reported no outputs while
the target declares `MAGUS.md`. It now uses `buildStep`, the same fold the cache
keys and snapshots, so a target's description and the cache's plan agree.

That turned out to be a bug fix rather than a feature, which is the cheapest
possible version of this phase: no grammar change, no new verb, "fold don't add"
satisfied by folding into a command that already existed.

**Next step is blocked on a decision, not on code.** `magus run <target> -o json`
is not wired: it prints progress plus an output ref (`outbcab0124`) and ignores
the output arm entirely. So there is no structured run result to attach produced
artifacts to. Adding one runs straight into risk 2 above - `magus run <target>
[flags] [project...]` already takes trailing project args, so neither `magus run
build outputs` nor a chained form is unambiguous today.

Two ways forward, and this is the fork to settle before writing code:

1. **Wire `-o json` on `run`** to emit a structured result including the
   artifacts produced. No grammar change, no chaining, works with every existing
   invocation. Gets the agent payoff; drops Dagger's chaining entirely.
2. **Commit to chaining** with an explicit separator so trailing project args stay
   unambiguous. Closer to Dagger; a real CLI grammar change.

Recommendation: (1). The agent value is in the structured result, not the
chaining, and (2) can be layered on later if the chaining is genuinely wanted.
