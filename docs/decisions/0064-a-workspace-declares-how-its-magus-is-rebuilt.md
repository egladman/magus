---
title: "ADR 0064: a workspace declares how its magus is rebuilt"
order: 64
description: The guard stops deciding that a workspace is a checkout of magus by reading go.mod. A workspace that builds its own magus declares the lines that do it as checkout.rebuild in magus.yaml, read from the committed tree, and the guard prints and exempts exactly those lines. A workspace that declares nothing is told to install a release, as today.
tags: [adr, decision, guard, config, bootstrap, layering]
status: proposed
date: 2026-11-28
supersedes: "ADR 0045, section 7: the relink and bootstrap build ./cmd/magus, so Go names them"
---

# ADR 0064: a workspace declares how its magus is rebuilt

Evidence marks: **read** is source read at `bd632b0e0`, **ran** is a `magus refs
--occurrences` run on 2026-11-28. Nothing here is built.

## Context

A workspace that is not a checkout of magus must never be handed a line only magus's own
source can run. That goal stands. Eli, 2026-11-28, on how it was met: "It should have
been an actual interface. Half-baked logic, slop code."

**The guard sniffs the module path.** `ownSourceRoot` (`internal/guard/bootstrap.go:212`)
compares the module path in `go.mod` with the guard's own. It has 8 call sites outside
tests in five guard files (**ran**), and two pass the answer on as a boolean `own`. A
fork under another module path is told to install a release (**read**).

**Go carries this repository's commands and flags** into every consumer's binary
(**read**; counts **ran**, tests excluded):

| what Go holds | where | uses |
| --- | --- | --- |
| `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .` | `ward.BootstrapCommand` | 10 in 6 files |
| `GOEXPERIMENT=jsonv2 go build -trimpath -o magus ./cmd/magus` | `ward.RelinkAt` | 5 in 4 files |
| an inline Buzz program importing `./hack/policy/stale` | `ward.RelinkAndStampAt` | 3 in 2 files |
| the bootstrap aimed with `go -C` | `config.SourceCommand` | 3 in 2 files |
| matchers for those lines | `bootstrapsMagus`, `recoversMagus`, `linksMagus` | the raw-tool deny |
| `--here --verify-ready` and `--here`, appended to any declared hook | `config.ReadyCheckoutCommand`; `buildRemedy` in `internal/job/store.go` | 3 |
| four rule texts saying "a checkout of magus" | `internal/guard/catalog.go` | the rule docs |

**The same file states the rule this breaks**: "magus never guesses a workspace's own
rebuild from its layout; a workspace that has one says so"
(`internal/ward/version.go:167-169`).

**Only one existing declaration reaches far enough.** `relinks` on `go-build` needs a
loaded magusfile. The placement script's `SERVES` line is read from the scripts index,
which only a binary that loads the tree builds. `checkout.bootstrap` in `magus.yaml` is
read when nothing else loads.

**Go already measures whether a tree carries a contract.** For `contract.Check`, "a tree
that carries none is no error and never differs". The skew rules need no module test.

**The precedent is #1014** (`175f2e123`): each VCS driver reports its own commands.

## Decision

A workspace that builds its own magus declares the lines that do it. The guard reads the
declaration from the committed tree, prints it and exempts exactly it. A workspace that
declares nothing is told to install a release with `config.ReleaseCommand`. No code asks
whether the workspace is magus.

### 1. `checkout.rebuild` in magus.yaml

```yaml
checkout:
  bootstrap: hack/dev/bootstrap-checkout.buzz
  rebuild:
    - [GOEXPERIMENT=jsonv2, go, -C, "{checkout}", build, -trimpath, -o, magus, ./cmd/magus]
    - ["{checkout}/magus", buzz, -C, "{checkout}", hack/dev/stamp-binary.buzz]
    - ["{checkout}/magus", buzz, -C, "{checkout}", hack/dev/bootstrap-checkout.buzz]
  rebuild_uncached:
    - [GOEXPERIMENT=jsonv2, go, -C, "{checkout}", run, -trimpath, ./cmd/magus, run, go-build, --no-cache, .]
```

Each entry is one command as a list of words. A leading `NAME=value` word is
environment. `{checkout}` in a word is bound to the checkout root, and it is the only
placeholder: the guard can run from any directory, so every line aims with `-C`. The
lines run in order, each alone on its line, and the first must need no magus. Go knows
no flag, no toolchain and no step name.

`rebuild` is the standard repair and never skips a cache. `--no-cache` skips magus's own
target cache, so it is the fallback for a cache that is suspect. The fallback is a
second declared list, `rebuild_uncached`, named for what it does. Every remedy prints it
after the standard lines, named as the line to run if they fail. That is simpler than a
line named only after a failure: the guard keeps no record of what ran, and a line it
exempts must be declared either way.

Three things in these lines are deliberate:

- `-trimpath` stays. It strips local paths from the binary and does not bypass Go's
  build cache, and the magusfile's `go-build` passes it too, so the two share entries.
- `GOEXPERIMENT=jsonv2` stays. magus imports `encoding/json/v2`, which does not compile
  without the experiment, and `go.mod` cannot set it. It now lives in this repository's
  declaration and not in the binary.
- The ready line passes no flag. The script's flags are renamed in a separate change:
  `--here` goes (the current directory is the default), building only when `./magus` is
  not at HEAD becomes the default so `--verify-ready` goes, and always building becomes
  `--force`.

### 2. `Rebuild` is a field on `config.Checkout`, read by `config.LoadCheckout`

`config.Checkout` already holds `Bootstrap` and gains `Rebuild`. No new type and no
interface. `config.LoadCheckout(root string) (Checkout, error)`, the twin of
`config.LoadAgents`, replaces `CheckoutHookAt`. It stays lenient: it reads `checkout:`
past keys this binary lacks. Unreadable YAML, a hook path that leaves the workspace, or
a rebuild entry that is not a non-empty list of words is an error, never "". A missing
`checkout:` section is an empty `Checkout` and no error.

Names take no `At`, `From` or `For` suffix. `RequiredVersionAt` is the package's
remaining `At` reader and is out of scope.

### 3. The declaration is read from the committed tree

The guard reads `checkout:` at HEAD. A leased worker's call reads it at its row's
authority (ADR 0045). It is never read from the working tree.

The threat: a declared line is exempt from the refusals that hold while the binary
cannot load the tree. Read from the working tree, any session could exempt any command
by editing `magus.yaml` when the guard can judge least.

A root no version control claims has no committed tree. It declares nothing and gets the
release line.

### 4. magus adds no argument to a declared line or hook

`ReadyCheckoutCommand` and `buildRemedy` stop appending `--here` and `--verify-ready`.
A remedy prints the declared lines as written. This repository's ready step is the last
line of its own `rebuild` list.

### 5. What Go keeps and loses

Go keeps the contract measurement and its refusal; reading, binding and printing the
lines; the exemption for a line equal to a declared one, alone on its line, while the
binary is absent or stale; who may run it (the root and the integrator, never a leased
worker); `relinks`; and the placement script found by `SERVES`.

Go loses:

- `ownSourceRoot`, the guard's `ownModule` and the `own` parameter;
- `ward.BootstrapCommand`, `relinkCommand`, `RelinkAt`, `stampScript`, `stampAt` and
  `RelinkAndStampAt`;
- `config.SourceCommand`, `bootstrapIn`, `CheckoutHookAt` and `ReadyCheckoutCommand`;
- `bootstrapParts`, `bootstrapLine`, `bootstrapsMagus`, `recoversMagus` and `linksMagus`;
- the hook flags in `buildRemedy`;
- the commands in the four catalog texts.

`ownSourceRoot` survives nowhere. `rankOwnBuild` ranks the declared line: a raw-tool
deny of a line equal to one becomes an advisory for the root and the integrator and the
worker's refusal for a lease. `judgedContract` and `denyContractPick` ask whether the
tree carries a contract.

### 6. The stamp stays a line that runs no target

`TestTheStampLinksTheTreesOwnStampWithoutATargetRun` pins it today: "a target run
regenerates generated output". The declared stamp line runs a `hack/` script holding
what `stampScript` holds now, and a test beside that script asserts the same. #1023's
change, which ran the target and deleted the test, is not taken.

### 7. Worker, integrator and consumer

- **Root and integrator** get the declared lines. The integrator may also run the
  target that declares `relinks` once the tree loads.
- **A leased worker** asks the main session for a placement, as today. The condition
  changes from `own` to "the workspace declares a rebuild".
- **A consumer** declares nothing and sees what it sees today: "install a magus that
  loads this workspace with `magus self update`". `ConsumerWorkspaceSuite` (#1023, open)
  pins that. The build widens it to every guard denial and fails on output holding
  `cmd/magus`, `go-build`, `hack/` or `GOEXPERIMENT`.

### 8. The break

Recorded per
[A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
a `changes/unreleased/` note with a Breaking line, and ADR 0045 section 7 amended in the
same commit. If a comparison gate reports a finding, a person adds the deviation line.

- A checkout of magus whose HEAD is older than the key gets the release line from a
  newer binary. Bring the base in. No fallback reads `go.mod`.
- An uncommitted `checkout:` edit has no effect on the guard.
- The `go run` bootstrap with `--no-cache` is the declared fallback, no longer the
  standard line. `AGENTS.md` and the bootstrap scripts change in the same commit.
- A hook that relied on magus appending `--here` or `--verify-ready` declares them.
- `CheckoutHookAt` answered "" for a bad path; `LoadCheckout` errors.
- MGS1021's advice drops its "built from source" line.

### State

| item | enforcement | State |
| --- | --- | --- |
| `config.Checkout.Rebuild`; `config.LoadCheckout` replaces `CheckoutHookAt` | config tests beside `LoadAgents`'s: lenient past unknown keys, an error on bad YAML, an escaping path or a malformed entry | planned |
| the guard reads `checkout:` at HEAD, a leased call at its row's authority | a guard case: an uncommitted `rebuild` edit exempts nothing | planned |
| magus's own `magus.yaml` declares link, stamp and ready lines, each aimed with `-C {checkout}` | a test beside the stamp script that it runs no target | planned |
| a declared fallback list is printed after the standard lines and exempt like them; only it passes `--no-cache` | a guard case: the remedy names both, the standard first | planned |
| magus adds no argument to a declared line or hook | `ReadyCheckoutCommand` gone; `buildRemedy` prints the lines as declared | planned |
| `ownSourceRoot`, `own` and the Go commands and matchers are deleted | `magus refs` finds none | planned |
| the skew rules ask whether the tree carries a contract | a consumer fixture with a leased row is never refused | planned |
| a declared line is exempt for the root and the integrator, refused to a leased worker | the `TestRankOwnBuild*` cases, moved to declared lines | planned |
| the four catalog texts quote no command | the generated rule docs | planned |
| a consumer sees only the release line | `ConsumerWorkspaceSuite`, widened to every denial | planned |
| Go core names nothing of this repository; the remaining leaks only shrink meanwhile | the `core-names-nothing-of-this-repository` lint under `hack/lint` (#1026) | planned |
| the break is recorded and ADR 0045 section 7 amended | the `changes/unreleased/` note; `adr-template` | planned |

### Principles checked

- "Magus is its own first consumer" (#1026, a principles page not yet on `main`): this
  repository gets its rebuild as any workspace would, by declaring it.
- [Told, never guessed](../design/judgment/told-never-guessed.md): the current code
  reads a file to guess where it runs. The workspace now says.
- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  the page this strains. A list of lines is a short sequence in configuration. It holds
  because each entry is an argv with no condition; anything that branches stays in Buzz.
- [No silent defaults](../design/judgment/no-silent-defaults.md): no declaration is a
  stated outcome, and a malformed one is an error.
- [Enforcement, not pinky promises](../design/principles/enforcement-not-pinky-promises.md):
  the lint and the consumer suite hold the layering.

## Alternatives

**A Buzz `SERVES` script, or a field beside `relinks`.** Neither adds a YAML key. Both
lost on reach: the scripts index and target policy exist only after a binary loads the
tree (`noPlacementScript` tells its reader to run `magus graph build`). Reopen if either
is read from the tree without a load.

**Read the declaration from the working tree**, as `checkout.bootstrap` is today. It
lost to the threat in section 3. Reopen with a way to judge the edit while nothing
loads.

**A new type or an interface.** The struct exists, and an interface with one
implementation hides nothing (Eli, 2026-11-28). Reopen if a second source of the lines
appears.

**Keep `ownSourceRoot` behind one named type, or move its branches into `hack/policy`.**
The first keeps the guess and the commands. The second cannot run: a guard rule needs
policy loaded, and these verdicts exist for when it is not.

## Consequences

A consumer's binary holds no line or flag that only this repository can run, and a lint
and a test fail when one comes back. A fork of magus gets its own lines by declaring
them.

A change to the rebuild takes effect when it is committed. A declared line is still an
exemption a workspace writes for itself, so `checkout:` is reviewed like guard policy.

Every repair in this repository now runs its ready line. Today the relink and stamp
stand alone when a binary is present.

The build cannot merge in parts without leaving two mechanisms.

## Open questions

- `LoadCheckout(root)` names a root, and the guard needs the bytes at HEAD or at an
  authority. The door that hands committed bytes to the same decoder is not named.
- Whether link, stamp and ready equal today's bootstrap and ready on a fresh checkout.
  The bootstrap runs `go-build`, which also generates. Not run.
- What the ready line costs in a repair made in the middle of work. Not measured. If it
  is too much, this repository's list drops it and Go does not change.
- `magus vcs checkout` and `magus job fork --checkout` run the hook with `--checkout
  <dir> --install <p,q|none>`, the contract `config.Checkout` documents. Whether that
  call also moves into a declaration is not decided here.
- `recoversMagus` also allows a hand-typed `go generate` for the root and the
  integrator. It has no declared home.
- `authorityContract` is assumed to answer as `contract.Check` does for a tree with no
  contract. Not read.
