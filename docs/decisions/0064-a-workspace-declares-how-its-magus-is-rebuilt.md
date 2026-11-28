---
title: "ADR 0064: a workspace declares how its magus is rebuilt"
order: 64
description: The guard stops deciding that a workspace is a checkout of magus by reading go.mod. A workspace that builds its own magus says so in magus.yaml, as an ordered list of command lines beside checkout.bootstrap, and the guard prints and exempts exactly those lines. A workspace that declares nothing is told to install a release, as it is today. Go keeps the measurement and the refusal and loses every command of this repository.
tags: [adr, decision, guard, config, bootstrap, layering]
status: proposed
date: 2026-11-28
supersedes: "ADR 0045, section 7: the relink and bootstrap build ./cmd/magus, so Go names them"
---

# ADR 0064: a workspace declares how its magus is rebuilt

Evidence marks: **read** is source read in this checkout at `bd632b0e0`, **ran** is a
`magus refs --occurrences` run made for this record on 2026-11-28. Nothing here is built.

## Context

A workspace that is not a checkout of magus must never be handed a line only magus's own
source can run. That goal stands. Eli, 2026-11-28, on how it was met: "It should have
been an actual interface. Half-baked logic, slop code."

**The guard sniffs the module path.** `ownSourceRoot` (`internal/guard/bootstrap.go:212`)
reads `go.mod` and compares its module path with the guard's own (**read**). It has 8
call sites outside tests in five files: `bootstrap.go:435`, `load_contract.go:127`,
`stale_binary.go:77`, `:863` and `:900`, `unjudged_hook.go:203`, `workspace_rule.go:368`
and `:628` (**ran**; the review of 2026-11-28 counted 7). Two of them turn the answer
into a boolean `own` that `denyUnloaded` and `binaryRemedy` pass on.

**The test is wrong in one direction already.** It is string equality on a module path.
A fork of magus under another module path builds its own binary and is told to install a
release (**read**).

**Go carries this repository's commands.** Each is compiled into the binary every
consumer installs (**read** for the text, **ran** for the counts, tests excluded):

| what Go holds | where | uses |
| --- | --- | --- |
| `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .` | `ward.BootstrapCommand`, `internal/ward/version.go:106` | 10 in 6 files |
| `GOEXPERIMENT=jsonv2 go build -trimpath -o magus ./cmd/magus` | `ward.RelinkAt`, `version.go:117` | 5 in 4 files |
| an inline Buzz program that imports `./hack/policy/stale` and links `./cmd/magus` | `stampScript`, `ward.RelinkAndStampAt`, `version.go:145` | 3 in 2 files |
| the same bootstrap, aimed with `go -C` | `config.SourceCommand`, `internal/config/stale_remedy.go:33` | 3 in 2 files |
| three matchers for those lines: `bootstrapsMagus`, `recoversMagus`, `linksMagus` | `internal/guard/bootstrap.go:98-159` | the raw-tool deny |
| four rule texts that say "a checkout of magus" | `internal/guard/catalog.go:547`, `:562`, `:623`, `:624` | the generated rule docs |

`hack/dev/bootstrap-checkout.buzz` holds a second copy of the bootstrap line as
`bootstrapArgv()`, with a comment naming the Go constant it mirrors (**read**).

**The branches are hard to follow.** `binaryRemedy` tests `own && hasBinary &&
ownSourceRoot(root)` and then `own && hasBinary`. Its caller at `workspace_rule.go:628`
passes `ownSourceRoot(j.root)` and `j.root`, so from there the second arm cannot run
(**read**). A reader has to work out which of two roots each answer came from.

**The same file already states the rule this breaks.** Sixty lines below
`BootstrapCommand`, on `StaleBuild.Scripts`: "magus never guesses a workspace's own
rebuild from its layout; a workspace that has one says so" (`version.go:167-169`,
**read**).

**Four partial declarations exist.**

- `magus.yaml` declares `checkout.bootstrap: hack/dev/bootstrap-checkout.buzz`.
  `config.CheckoutHookAt` reads it leniently, like `RequiredVersionAt`: "a binary too old
  to decode the rest of the file still reads its floor" (`stale_remedy.go:40-62`, **read**).
- `go-build` declares `"relinks": "./magus"` (`magusfile.buzz:241`), and the guard reads
  it through the target policy index (`runRelinks`, `target_policy.go:138`).
- `hack/dev/bootstrap-worktree.buzz` declares
  `SERVES = ["rule:stale-binary -- --job {lease} --from {checkout}"]`. The worker's
  placement line comes from it (`placementArgv`, `stale_binary.go:957`). ADR 0045 marks
  that row done.
- `hack/dev/bootstrap-checkout.buzz` declares `SERVES = ["code:MGS1021 -- --checkout
  {checkout}"]`, which `StaleBuild.Scripts` prints.

**Each declaration has a different reach.** A `SERVES` line is read from the scripts
index in the cache (`hint.ServingScriptArgv(workspaceCacheDir(root), ...)`), which a
binary that loads the tree builds. The refusal for a missing placement script says so:
declare one "and run `magus graph build`" (`noPlacementScript`, **read**). A target's
policy needs the magusfile loaded. `magus.yaml` is the only one of the three a binary
reads when it can load nothing else, and a fresh checkout has no index and no binary.

**Go already measures whether a tree carries a contract.** `contract.Check` compares the
binary's embedded contract with the tree's. "A tree that carries none is no error and
never differs" (`internal/contract/contract.go:205-209`, `ErrNotCarried`, **read**). So
the skew measurement needs no module test to stay quiet in a consumer workspace.

**ADR 0045 kept these lines in Go on purpose.** Section 7: "A path in magus's own module
stays named, with a comment saying why", because "no workspace can declare where a
binary embeds from". The contract half of that still holds. The command half is what the
review of 2026-11-28 and the table above answer: a command is something a workspace can
declare, and four declarations already do.

**The precedent is #1014.** `types.VCSIdentity` gained `OperationCommands` and
`FoldCommand`, "so no caller keeps one backend's list" (`types/vcs.go:67-71`, merged as
`175f2e123`). Callers ask the driver. None branches on which backend it is.

**The layering rule** is the review's: Go measures, records, compares and refuses on
declared input. `hack/` Buzz holds this repository's policy. Spells hold language
knowledge. A Go line naming `hack/`, `go-build`, `./cmd/magus` or `GOEXPERIMENT` is this
repository's policy in every consumer's binary
(`cli-review-2026-11-28.md`, section 3 and rollback 10).

## Decision

A workspace that builds its own magus declares the lines that do it. The guard reads the
declaration, prints it and exempts exactly it. A workspace that declares nothing is told
to install a release with `config.ReleaseCommand`. No code asks whether the workspace is
magus.

### 1. The declaration is a list of lines in magus.yaml

It sits under `checkout:`, beside `bootstrap`. Each entry is one command as a list of
words. A leading `NAME=value` word is environment. `{checkout}` is bound to the checkout
root, as a `SERVES` line binds it today. The lines run in order, each alone on its line.
The first must need no magus. The key's name is Eli's pick (Open questions); it is
written `rebuild` below.

```yaml
checkout:
  bootstrap: hack/dev/bootstrap-checkout.buzz
  rebuild:
    - [GOEXPERIMENT=jsonv2, go, -C, "{checkout}", build, -trimpath, -o, magus, ./cmd/magus]
    - ["{checkout}/magus", buzz, -C, "{checkout}", hack/dev/stamp-binary.buzz]
```

Why `magus.yaml`: it is the one declaration a binary can read when it loads nothing
else, and it is the file a person already edits for `checkout.bootstrap`. Why a list of
whole lines: Go then knows no flag, no toolchain and no step name. It does not know that
one line links and the next stamps.

The value is read leniently, as `CheckoutHookAt` reads its neighbor. An entry that is
not a list of words, or an empty list, is refused by name. It is never read as no
declaration.

### 2. One value answers every caller

The five guard files stop threading a boolean. One value, read once per judged call,
holds what the workspace declares about getting a magus that loads it: the rebuild lines
bound to a root, the ready line from `checkout.bootstrap`, and the release line. A
caller asks it for the lines to print and for whether a typed line is one of them. With
no declaration it answers with the release line, so no caller branches on "declared".
Its name and whether it is a Go interface with two implementations or one struct are
Eli's pick (Open questions).

### 3. What Go keeps and what it loses

Go keeps:

- the contract measurement (`internal/contract`) and the refusal while it differs;
- "this tree carries a contract" as the test for the skew rules, which is
  `ErrNotCarried` and no module path;
- reading the declaration, binding `{checkout}`, quoting and printing each line;
- the exemption: a line equal to a declared line, alone on its line, runs while the
  binary is absent or stale;
- who may run it: the root and the integrator, never a leased worker (ADR 0045,
  sections 4 and 5);
- `relinks` on a target, and the placement script found by `SERVES`.

Go loses `ownSourceRoot`, `ownModule` in the guard, the `own` parameter,
`ward.BootstrapCommand`, `relinkCommand`, `RelinkAt`, `stampScript`, `stampAt`,
`RelinkAndStampAt`, `config.SourceCommand`, `bootstrapIn`, and the matchers
`bootstrapParts`, `bootstrapLine`, `bootstrapsMagus`, `recoversMagus` and `linksMagus`.
The four catalog texts say "a workspace that declares its rebuild" and quote no command.

`ownSourceRoot` survives nowhere. `rankOwnBuild` keeps its job under the declaration: a
raw-tool deny of a line equal to a declared rebuild line becomes an advisory for the
root and the integrator, and the worker's refusal for a lease. A workspace with no
declaration gets no such refinement, so its raw `go build` is refused like any other raw
tool. The two skew rules that asked `ownSourceRoot` (`judgedContract`,
`denyContractPick`) ask whether the tree carries a contract.

### 4. The stamp step stays a line that runs no target

Today's stamp is an inline Buzz program run by the binary the relink just linked. It
runs no target, pinned by `TestTheStampLinksTheTreesOwnStampWithoutATargetRun`: "a target
run regenerates generated output". A repair made in the middle of work must not rewrite
tracked files.

The declared rebuild keeps that property. The second declared line runs a script under
`hack/` that holds what `stampScript` holds now. The script's path is this repository's
to name. The Go test goes with the Go string, and a test beside the script asserts the
same thing about the script. #1023's change, which ran the target and deleted the test,
is not taken.

### 5. The worker and the integrator still differ, and both read declarations

- **Root and integrator**: the rebuild lines, then the ready line when the checkout had
  no binary. The integrator may also run the target that declares `relinks` once the
  tree loads. Unchanged in shape.
- **Leased worker** in a workspace that declares a rebuild: asks the main session for a
  placement, served by the script declaring `SERVES rule:stale-binary`. Unchanged. The
  condition changes from `own` to "the workspace declares a rebuild".
- **Leased worker** elsewhere: "ask the main session to install a magus that loads this
  workspace". Unchanged.

The two paths have two homes for a reason. The rebuild must be readable with nothing
loaded, so it is YAML. The placement is run by the main checkout's binary, which loads,
so the scripts index serves it.

### 6. A consumer sees the same thing

Before and after, a workspace that declares nothing gets the last arm of `binaryRemedy`:
"install a magus that loads this workspace with `magus self update`", pinned by
`required_version`, and `repairLines` returns that one line. `ConsumerWorkspaceSuite`
(#1023, open, not on `main`) pins that outcome. The build widens it to run every guard
denial in a consumer fixture and to fail on any output holding `cmd/magus`, `go-build`,
`hack/` or `GOEXPERIMENT`.

### 7. The break

Recorded per
[A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md).

- A checkout of magus at a commit older than the key declares nothing. A newer binary
  judging it prints the release line. The fix is to bring the base in. No fallback reads
  `go.mod` for older checkouts.
- The hand-typed bootstrap, `go run -trimpath ./cmd/magus run go-build --no-cache .`, is
  no longer exempt. The declared lines are. `AGENTS.md` and the bootstrap scripts change
  in the same commit.
- MGS1021's advice drops its "built from source" line. A declared script still prints.

The change carries a `changes/unreleased/` note with a Breaking line and amends ADR 0045
section 7 in the same commit. If a comparison gate reports a finding, a person adds the
deviation line.

### State

| item | enforcement | State |
| --- | --- | --- |
| `magus.yaml` holds the rebuild lines under `checkout:`, read leniently; a malformed entry is refused by name | a config test beside `TestCheckoutHookAt`'s cases | planned |
| magus's own `magus.yaml` declares its link and stamp lines; the stamp is a script under `hack/` | a test beside the script that it runs no target | planned |
| one value answers the guard's callers; no declaration answers with the release line | guard suite cases for declared and undeclared roots | planned |
| `ownSourceRoot`, `own` and the Go command constants and matchers are deleted | `magus refs` finds none; the analyzer below | planned |
| the skew rules ask whether the tree carries a contract | a consumer fixture with a leased row and no contract is never refused | planned |
| a declared line is exempt for the root and the integrator and refused to a leased worker | the cases `TestRankOwnBuild*` hold today, moved to declared lines | planned |
| the four catalog texts name the declaration and quote no command | the generated rule docs | planned |
| a consumer workspace sees only the release line | `ConsumerWorkspaceSuite`, widened to every denial | planned |
| no Go under `internal/`, `cmd/`, `libs/` or `std/` holds `hack/`, `go-build`, `./cmd/magus` or `GOEXPERIMENT` | a `hack/lint` analyzer with a shrink-only baseline in the root `analyzers.yaml` | planned |
| `bootstrap-checkout.buzz` reads the declared line and drops its copy | `magus refs bootstrapArgv` finds none | planned |
| the break is recorded and ADR 0045 section 7 amended | the `changes/unreleased/` note; `adr-template` | planned |

### Principles checked

- [Told, never guessed](../design/judgment/told-never-guessed.md): this is the page the
  current code breaks. It reads a file to guess where it runs. The workspace now says.
- [A file a person edits is YAML](../design/principles/a-file-a-person-edits-is-yaml.md):
  the declaration is a key a person edits in `magus.yaml`.
- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  this is the page the decision strains. A list of lines is a short sequence in
  configuration. It holds because each entry is an argv with no condition, and anything
  with a branch stays in the Buzz script the last line runs.
- [No silent defaults](../design/judgment/no-silent-defaults.md): no declaration is a
  stated outcome, the release line, and a malformed one is an error.
- [Delete before you add](../design/principles/delete-before-you-add.md): one key is
  added beside an existing one. Five Go constants, five matchers and one predicate go.
- [Abstract at the right layer, or not at all](../design/principles/abstract-at-the-right-layer.md):
  one value hides where a remedy comes from. This is why section 2 leaves "interface or
  struct" open: a Go interface with one implementation would hide nothing.
- [Measures in, verdicts out](../design/principles/measures-in-verdicts-out.md): Go
  measures skew and refuses on it. What repairs it in this repository is the
  repository's.
- [One source, every door](../design/principles/one-source-every-door.md): the guard,
  MGS1021 and the bootstrap script read one declaration. Today there are two copies of
  the line.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md):
  every refusal still ends in a line to run.
- [Enforcement, not pinky promises](../design/principles/enforcement-not-pinky-promises.md):
  the analyzer row and the consumer suite hold the layering. Prose in `AGENTS.md` did
  not.
- [Break now, hold still later](../design/principles/break-now-hold-still-later.md):
  no reader of `go.mod` stays for older checkouts.
- "Mechanisms live below the magusfile" is Eli's ruling and has no design page yet. The
  decision follows it: reading, binding, printing and exempting are in Go, and the
  workspace supplies only the lines.

## Alternatives

**A Buzz `SERVES` script, as the placement uses.** It adds no key. It lost on reach: a
`SERVES` line is found in the scripts index, which only a binary that loads the tree
builds, and running a script needs a magus. The rebuild is needed exactly when neither
holds. Evidence: `placementArgv` reads the cache, and `noPlacementScript` tells the
reader to run `magus graph build`. Reopen if the index is ever read from the tree
without a load.

**A field on the target that declares `relinks`.** The build knowledge already sits on
`go-build`. It lost for the same reason: target policy is read from a loaded magusfile.
`relinks` stays for what it does now, which is to mark the run that replaces the
binary. Reopen with the same condition.

**Two keys, a link line and a stamp script.** Closer to today's wording, "relink, then
stamp". It lost because Go would keep both words and a reader for each, and the next
repository's rebuild may have one step or three. Reopen if a verdict ever needs to tell
the steps apart.

**One link line, with the stamp inside the `checkout.bootstrap` hook.** One key and no
new script. It lost because the hook also installs dependencies, harness rows, the
graph and the symbol digest, so a repair in the middle of work would pay for all of it,
or Go would pass the hook a mode flag and know the hook's flags. Reopen if a measured
`--verify-ready` on a ready checkout costs about what the stamp does.

**Keep `ownSourceRoot` behind one named type.** The smallest change. It lost because the
guess stays a guess: a fork under another module path is still told to install a
release, and the commands stay in every consumer's binary.

**Move the branches into `hack/policy` guard rules** (the review's rollback 10 as
written). A guard rule runs only when policy loads, and these verdicts exist for when it
does not. The declaration is the part of that rollback that can work unloaded. Reopen
for the verdicts that fire only on a loaded tree.

**Keep ADR 0045 section 7 as it is.** Its reason was that no workspace can declare
where a binary embeds from. That is still true of the contract and was never true of a
command. New evidence since: the counts above, the review of 2026-11-28, and Eli's
ruling on the mechanism.

## Consequences

A consumer's binary holds no line that only this repository can run, and a test fails
when one comes back.

A fork of magus, or any workspace that builds its own magus, gets its own lines by
declaring them. Nothing in Go is specific to this repository's rebuild.

The declared lines are exempt from the stale-binary and contract-skew refusals for the
root and the integrator. That is an exemption a workspace writes for itself. Today
`magus.yaml` is a denied write path for a leased worker ("workspace configuration"), and
the root is a person or the main session. The guard reads the key from the working tree,
so an uncommitted edit changes what is exempt.

The guard's texts change, and so do the generated rule docs, `AGENTS.md`'s Binary
section and the bootstrap scripts.

A checkout of magus older than the key is told to install a release until its base is
brought in.

The build is wide: five guard files, `internal/ward`, `internal/config`,
`internal/contract`, `row_unknown.go`, `cost.go`, their tests and the catalog. No part of
it can merge alone without leaving two mechanisms.

## Open questions

- **The key's name**, under `checkout:`. Options: `rebuild`, `relink`, `build`.
  `relink` matches the target's `relinks` and may be confused with it. `build` is the
  plainest and the least specific.
- **The Go value's name and kind.** Options: `config.Rebuild` with `config.RebuildAt(root)`
  after `CheckoutHookAt`; `config.MagusSource`; `ward.BinarySource`. And whether it is a
  Go interface with a declared and a release implementation, or one struct. No symbol
  with these names exists (`magus query "kind=symbol"`, 2026-11-28). `magus query
  "kind=config"` returned nothing, so config keys were checked by reading
  `config.Checkout`.
- **How a line is aimed at a root.** `{checkout}` in the declared words is decided
  above. The other candidate is a bare line that the guard prefixes with `cd <root> &&`.
  The binding code in `internal/hint` was not read for this record.
- **Whether link, stamp and ready equal today's bootstrap and ready on a fresh
  checkout.** The bootstrap runs `go-build`, which also generates. The link uses the
  checked-in generated files. Not run. If a gap shows, the hook closes it, and no second
  key is added.
- **The hand-typed `go generate` recovery.** `recoversMagus` allows `go generate` of
  local packages for the root and the integrator. It rebuilds no binary and has no
  declared home here. `bootstrap-checkout.buzz` already runs the `go:generate` lines when
  a build fails to compile; whether that covers every use was not checked.
- **The ready line's flags.** `config.ReadyCheckoutCommand` passes `--here
  --verify-ready`, which `config.Checkout` does not document as the hook's contract. They
  are this repository's script's flags in Go, and this record leaves them.
- **`authorityContract` in a consumer.** Section 3 assumes it answers `ErrNotCarried`
  for a tree with no contract, as `contract.Check` does. Not read.
- **Whether the declaration is read at a leased row's authority**, as policy is (ADR
  0045 section 6), or from the working tree as `checkout.bootstrap` is today.
- ADR 0045 is `proposed`. This record amends one sentence of it and relies on the rest.
