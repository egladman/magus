---
title: ctx.observes - reconstruction
status: implemented (mechanism); one half deliberately left out
---

# ctx.observes: declaring an external observed input

## The reconstructed intent

A target's answer can depend on a fact that is not in the tree. magus keys on
files, the environment allowlist, exec overrides, deps, and tool-version probes -
so a fact outside all five is invisible, and a cache hit replays an answer that
was computed against a different world.

The only control that exists for this today is `skip_cache`, which forfeits
caching forever to avoid staleness. `ctx.observes` makes the invisible input
visible instead, so caching becomes correct rather than forbidden: the observed
value joins the cache key, an observation that moves is a miss, and an
observation that holds still replays.

## The evidence

The design was lost. Its plan file exists on no branch, and the API appears in no
file in this tree - `observes` matches only prose in comments. What survives is
the problem statement, in `magusfile.buzz` at the `image-scan` policy:

> Keyed on the image and this tree, but the ANSWER also depends on trivy's
> vulnerability database, which changes daily and is not an input magus can see.
> A hit would report yesterday's CVEs against today's image.

Two more places in the tree state the same principle from the other side, and
they are what fixes the shape of the feature:

- `types/describe.go`, on `EnvAllow`: the value is "only knowable at run time, so
  the NAME is what is declared statically and the value is read when the key is
  computed. That is what lets a target whose env is genuinely derived from the
  environment stay cacheable instead of having to opt out of the cache entirely."
  That last clause is this feature's purpose, one class of fact over.
- `docs/concepts/cache.md`, "The opposite failure: tools outside the key
  entirely": under-invalidating is "quiet and much worse", and magus answers it
  for TOOLS with a spell-level version probe. `ctx.observes` is the same answer
  for a fact that belongs to no spell.

## The family this joins

Three declarations already put a non-source fact in the key. `observes` is the
fourth, and it differs from each in exactly one dimension:

| declaration                   | declared statically | value comes from                | key line       |
| ----------------------------- | ------------------- | ------------------------------- | -------------- |
| `ctx.withEnv({K: V})`         | name and value      | the magusfile                   | `exec:env:K=V` |
| `ctx.envInputs("N")`          | the name            | the process env, at key time    | `env:N=v`      |
| spell `mgs_getVersionProbe`   | the argv            | a subprocess, at key time       | `tool:s:v`     |
| **`ctx.observes(k, v)`**      | **key and value**   | **the magusfile**               | **`obs:k=v`**  |

Mechanically `observes` is `withEnv`'s twin. Semantically it is its opposite:
`withEnv` changes what the tool RUNS WITH, while an observation changes nothing
about execution at all. It is a pure statement that the answer depends on this
fact, which is why it needs its own line class rather than reusing `exec:`.

## What was built

`ctx.observes(key, value)`, both string literals, on the target's own `ctx`.

1. **Binding** (`internal/interp/bindings/target.go`) - a no-op at run time,
   alongside its footprint siblings, and refused on a `magus\Exec` derivation.
2. **Static read** (`internal/describe/extract.go`) - literal args are paired into
   canonical `"key=value"` strings on `TargetGraphNode.Observations`. A computed
   argument, an aliased ctx, or an unpaired arg count trips `DynamicIO`, which is
   a hard load error: an observation the static read cannot see is not merely
   unread, it is a silent under-declaration, and that is the exact stale hit the
   feature exists to prevent.
3. **Resolution** (`describe.go`) - append-with-dedup into
   `Project.TargetObservations`, the same shape as `TargetExecOverrides`.
   Multiple calls accumulate. One key declared twice with the SAME value collapses;
   with two DIFFERENT values both survive into the key, which is what `appendUniq`
   over a canonical `"key=value"` already does for `withEnv`'s `"env:K=V"`. Neither
   "last wins" nor an error was invented for it: keeping both over-invalidates (the
   key moves when either moves) where dropping one would under-declare, and this
   file's governing asymmetry is that under-invalidating is the quiet failure.
4. **Fingerprint** (`run.go` `buildStep` -> `internal/cache/hash.go`) - folded
   onto `cache.Step.Observations`, hashed as `obs:key=value` from a sorted copy,
   immediately after the `env:` block.
5. **Explanation** - free, and that is the point of hashing it through
   `writeLine`. The pre-hash lines ARE the persisted explanation, so
   `PersistKeyInputs`, `magus query output <ref> --meta`,
   `describe target --cache --against`, and `DiffKeyInputs` (which derives its
   class generically from the prefix before the first `:`) all carry `obs:` with
   no new code and no new command.

`KeyVersion` is NOT bumped. An empty `Observations` writes no line, so every
existing entry hashes exactly as before - the same property the `charm:` and
`arg:` lines were added under.

## What was deliberately left out, and why

**The probe half.** The obvious next reach is a value the magusfile COMPUTES -
`ctx.observes("trivy-db", probe())` - and that cannot work here. The constraint is
structural rather than a matter of effort:

- `run.go:1201` passes the target handler as `cache.RunAll`'s `fn`. The body runs
  INSIDE the cache call, after the key is minted.
- `internal/describe/extract.go:234` already states the rule for `withEnv`: "the
  key is computed before the body runs, so a purely runtime override could never
  reach it".

So a value produced in the body reaches the key of the NEXT run, never its own -
which is precisely the stale answer `skip_cache` was protecting against. A
computed value is therefore rejected at load rather than silently accepted.

The seam for the missing half already exists and has no caller:
`cache.OnStep` (`internal/cache/options.go:97`, fired at `cache/cache.go:350`)
mutates the Step immediately before hashing. A probe channel would evaluate
there, exactly where `toolVersionsByProject` already spawns subprocesses for the
same reason. That is a separate unit: it needs a decision about which Buzz
evaluation phase a probe belongs to, and a probe budget, neither of which the
mechanism above depends on.

**`image-scan` stays on `skip_cache`**, with a one-line comment naming
`ctx.observes` as its successor. Not because trivy might be absent locally - that
would be acceptable for a target that already requires trivy - but because
without the probe half there is no expression that yields today's trivy DB id at
key time. A hand-written literal would be a stamp nobody remembers to bump, which
is a worse failure than the honest opt-out: it would report a cache hit and claim
the observation held. The demo is a test fixture instead.

**No new command, no new diagnostic code, no forecast-history change.**
`internal/ci/forecast/history.go` (Version 5) stores per-run outcome history and
is not where a key input belongs: the key-inputs sidecar is already the
per-key explanation store, it is already read by three surfaces, and observations
are a property of the KEY, not of a run.

## Where it is pinned

Each hop has a test at its own level, because the failure mode of a declaration
like this is that it reads as declared and resolves to nothing:

- `internal/describe.TestObservesExtraction` - the static read pairs literals,
  and rejects a computed value and an unpaired key.
- `magus.TestObservesReachesTheCacheKey` - a real magusfile through `Open` to the
  key a run would mint, so the hops are proven CONNECTED.
- `magus.TestObservesRejectsAComputedValue` - the load error.
- `internal/cache.TestObservationsChangeTheKey` - the key arithmetic, with every
  other Step field held fixed: moves on change, holds on repeat, order-independent,
  and present in the persisted key inputs.
- `cmd/magus` script `ctx_observes.txtar` - the CLI-visible half, and the demo
  that stands in for converting `image-scan`.

Regenerating after this lands: `types/buzzobject_gen.go`,
`internal/interp/bindings/gen/magus.go`,
`internal/spellruntime/gen/types/targetgraphnode.buzz`, and
`internal/spellruntime/gen/decls/magus.buzz` all mirror `TargetGraphNode` and
gain `observations`.

## Known gap found in passing (not this unit's)

`internal/dry/host.go`'s `buildCtx` binds only `needs`, `glob`, `hasCharm`,
`readsFiles`, `writesFiles`, `modifiesExistingFiles`. `envInputs`, `withEnv`, and
`withCwd` are missing, so a body calling one of them is not traceable by the dry
runner. `observes` was added there to keep this feature whole; the other three
are a pre-existing gap worth its own fix.
