---
title: "ADR 0045: a worker is judged by its checkpoint"
order: 45
description: The guard that judges a leased worker takes its authority from a revision the store records when the work is forked, never from the worker's own edits or commits. The worker never writes its judge. The integrator places the judging binary and the store records its hash, and a call is handed only to a binary a live row records. A worker that needs its own build makes it at a path no hook runs. The threat is mistakes and self-approval; containing deliberately hostile code is planned zero-trust work. Go core holds the primitives, the language spell names a build's inputs, and the workspace's Buzz decides who may build. An SDK caller plugs in its own rows through one reader interface.
tags: [adr, decision, guard, jobs, agents, leases]
status: proposed
date: 2026-11-12
---

# ADR 0045: a worker is judged by its checkpoint

## Context

The guard judges every call a worker makes, with two sources the worker can reach:

- **The guard policy.** This is the Buzz the workspace's magusfile imports for its guard,
  plus what that imports, plus data the policy reads through `fs` while it judges
  (`hack/policy/builds.buzz:9`). Every magus workspace has one.
- **The judging binary.** In a workspace that builds its own magus, the hook hands each
  call to the `./magus` of the call's checkout (`delegateToCheckout`,
  `cmd/magus/shell.go:1108-1139`). A workspace that installs magus has no judging binary
  in its tree.

Holes found on 2026-11-12, the first in an incident and the rest by two cold reviews of
this record's drafts:

- **A worker's build became the root's authority.** A worker rebuilt `./magus` from its
  branch, and that binary migrated the job store every worktree shares. Every magus
  refused the store with MGS3051 until a person restored it. a6f8c58c1 refuses that
  migration; nothing refuses the build.
- **A committed loosening is never pending.** `headPolicy` lets the working tree apply
  where it is stricter and sends a loosening to "the approved sources"
  (`internal/guard/workspace_rules.go:32-36`). `Pending` lists what differs from HEAD
  (`:100`), so once a worker commits, nothing is pending and its loosening applies.
- **Many doors write the judge.** fbacfdce1 lets a leased worker relink its own
  `./magus`. A worker also reaches it through `go generate` (advised through for every
  caller, `internal/guard/bootstrap.go:147-162`), `magus run go-build` in a checkout its
  job owns (`hack/policy/builds.buzz:83-101`), the bootstrap with `--here`
  (`bootstrap.go:505-520`) and test code its check runs. The hook hands a call to any
  executable `./magus` in any checkout of the repository (`shell.go:1118-1135`).
- **The checkpoint is client-declared.** `Job.Checkpoint` is what the forker sends
  (`types/job.go:762`, copied by `Declaration.Apply` at `:1923`), so a worker can fork a
  child naming its own commit. `JobWorktree.Base` is store-computed, but only a fork with
  `--checkout` has a `Worktree` (`types/job.go:944-946`).
- **The SDK judges differently from the hook.** It returns an error when the working tree's
  policy does not load (`judge.go:64-66`) where the hook judges with the approved sources
  (`workspace_rules.go:1269-1311`), and it loads policy before any lease is known
  (`judge.go:46-71`; the lease resolves inside `Judge`, `internal/guard/guard.go:957-971`).

Eli decided on 2026-11-12 that a worker must not rebuild a judge it can weaken, that the
rule must hold in any workspace rather than name this repository's paths, that the split
between Go and Buzz must be right, that the judgment must be usable from outside any one
agent host, and that a Go program embedding magus must be able to bring its own rows.

## Decision

### 1. The threat is mistakes and self-approval

The guard stops an honest but careless worker from rebuilding its judge, migrating state
the root owns, or approving its own loosening of a rule. A worker that runs code built to
defeat the guard, such as a test that writes the job store, is not contained by the guard.
That containment belongs to an operating-system sandbox around the worker's process, and
section 9 records it as planned work. Eli chose this on 2026-11-12.

### 2. Every fork records an authority, and the store computes it

The store records `Authority`, a full revision, on every row it writes from a fork, in a
field of its own and not in `Worktree`. For a fork by the root it is the root checkout's
committed revision at that moment. For a child it is its parent's `Authority`. The forker
is told by the transport (`RegisteredBy`), never by a field the forker sends, and a row's
declared `Checkpoint` never sets it.

A leased call whose row has no `Authority` is refused every state-changing line. The lines
that remedy it still pass, as `denyUndeclaredLease` passes them. A call with no lease keeps
today's authority, its checkout's committed revision.

A host that carries no lease for a worker makes that worker's calls look like the root's.
Such a host is unsupported for delegated work, and `magus doctor` says which host it is.

### 3. The policy waits on the authority

`Pending` lists every path that differs between the authority's revision and the working
tree, commits included. The approved sources are read at the authority's revision. The
pending cache key and the policy snapshot key carry the authority. A worker's committed
loosening stays pending until the root's authority holds it.

### 4. The judge is pinned, and no leased call writes it

The integrator places the judging binary in a job's checkout (`job.MirroredBinary`, or a
build from the authority's revision), and the store records the binary's path and content
hash on the row. The root's hook hands a call only to a binary at a path and hash a live
row records. Any other `./magus` is not run, and the session's own binary judges the call.
That one check covers every door in Context: a relink, `go generate`, `go-build`, the
bootstrap and a test that writes the file each leave a binary the hook does not run.

The hash is cached against the file's identity and size and modification time. A worker
can forge those, which section 1 accepts.

A worker that needs its own build, to try a flag it added, builds it at a product path no
hook executes. The workspace's build target declares that path beside the judge's, with the
sibling target that writes it: `go-build` declares `relinks: ./magus` and
`product: {"path": "bin/magus", "target": "go-build-product"}`. The path keeps the relinked
binary's file name, so the guard treats a line running it as a magus line for every lease
rule. A worker may run the product target and write the product path; its relink of
`./magus` is still refused. The hook hands no call to the product path, since no row pins it.

The contract digest stays. It compares the judging binary with the tree at the authority's
revision, so a worker's own edit to `api.lock` is its work, not skew.

### 5. The integrator relinks, re-pins and advances

The integrator is a row the root forks with `Integrates` set; a child cannot set it. In
its landing checkout it relinks the judge, the store records the new hash, and it advances
the `Authority` of each row whose work it lands. No other leased call does any of the three.
The root advances a row's authority when it merges into that row's checkout.

The doors are `magus job pin <job>` and `magus job advance <job>`. The fork pins the binary
it places, and so does `hack/dev/bootstrap-worktree.buzz`. The integrator runs both doors in
its landing checkout after it relinks there, and the root runs `advance` after a merge and
`pin` after it rebuilds a job's checkout. The store refuses both to any other bound caller.

Until the authority advances, the root's later commits count as the worker's own diff,
and the verdict says the authority is behind.

### 6. Policy and authority come from the row; paths resolve where the line runs

A leased call loads policy and authority from its row's checkout. ADR 0015 is unchanged:
a path resolves where its line runs. A state-changing leased line that lands outside its
row's checkout is refused, as `lease-checkout` already refuses a magus call there.

The lease is resolved before policy loads, so every door can choose the row first.

### 7. Three primitives in Go, inputs in the spell, policy in Buzz

- **Go core** holds the language-agnostic primitives: the authority of a call, the paths
  changed between a revision and the working tree, and the binary pin. The paths changed
  are an optional capability of the VCS driver (as `CheckoutIDReader` is), so a driver
  without it is not broken: the authority is unreadable there, and the verdict says so.
  Go names no path of the repository's own policy, such as a `hack/` script; those move
  to declarations. A path in magus's own module stays named, with a comment saying why:
  the contract mirrors the module's `go:embed` lines, and the relink and bootstrap build
  `./cmd/magus`, its main package. Each is used only in a checkout of magus, and no
  workspace can declare where a binary embeds from.
- **The language spell** names a build's inputs. The go spell answers which files a target
  that relinks compiles and embeds, and `job.ChangedBuildInputs`
  (`internal/job/worktree.go:478`) moves there, with the Buzz copies in
  `hack/dev/bootstrap-worktree.buzz` and `builds.buzz`.
- **The workspace's Buzz** decides who may build what. `builds.buzz` is rewritten in the
  same change: a worker builds only its product path, and the integrator relinks the judge.

### 8. Every door reaches the same judgment, and rows are pluggable

The hook, `magus shell`, the SDK's `Judge` ([ADR 0046](0046-the-judge-takes-any-agent-step.md)) and the Buzz guard module are doors
onto the one judgment `internal/guard` computes. The SDK judges with the approved sources
when the working tree's policy does not load, as the hook does.

Every lease rule reads rows: write paths, read-only, checks, `lease-checkout`, and the
authority and pin here. A Go program that embeds magus brings its own rows through one
interface in `types`, `JobReader`, and passes it with `magus.WithJobReader`. Its one
method, `List`, returns every row: a rule reads the lease's row and also its parent,
children and siblings, so a reader of one row would not serve them. magus's job store is
the default reader. The lease the caller puts in the context (`types.WithJobLease`) names
the embedder's own task, and the reader answers for it.

A caller with no row, such as CI judging a pull request, passes its authority on the
request: the revision of the branch the change targets. Otherwise a pull request would
approve its own loosening.

The binary pin is not pluggable. It guards the hook's hand-off between processes, and an
embedder that judges in its own process has nothing to hand off.

### 9. Zero trust is planned

Eli, 2026-11-12: agents and units of work are worth treating as zero trust. The planned
work contains a worker's own code (its tests, scripts and product build) so it cannot write
what judges it: the job store, the pin, the authority, or a binary the root builds from a
module cache workers share. This needs an operating-system sandbox around each command a
worker runs. That sandbox is a later decision, and section 1 holds until then.

### State

| item                                                                                         | State    |
| -------------------------------------------------------------------------------------------- | -------- |
| every fork records a store-computed `Authority`; a child inherits its parent's                 | done     |
| a leased row with no authority is refused state changes                                        | done     |
| `Pending` and the approved sources read at the authority; cache keys carry it                 | done     |
| an optional VCS capability lists paths changed between a revision and the working tree        | done     |
| the row records the judging binary's path and hash; the hook runs only a recorded binary      | done     |
| a build target declares a product path; the guard treats it as magus                          | done     |
| the integrator relinks, re-pins and advances; nobody else does                                | done     |
| a leased call loads policy and authority from its row's checkout                              | done     |
| the lease resolves before policy loads, at every door                                         | done     |
| the SDK judges with the approved sources when the working tree does not load                  | done     |
| a request carries an explicit authority for a caller with no row                              | done     |
| `types` row reader and `magus.WithJobReader`; the job store is the default                    | done     |
| `ChangedBuildInputs` moves into the go spell (`mgs_getScopes`); the Buzz copies go           | done     |
| `builds.buzz` rewritten to the product-path rule                                              | done     |
| Go names only its own module's paths, each saying why (`internal/contract`, `internal/ward`, `load_contract.go`) | done     |
| the worker placement script comes from the scripts index, not `stale_binary.go`              | done     |
| the worker relink of fbacfdce1 is removed                                                     | done     |
| a leased call's contract digest reads the tree at its row's authority                         | done     |
| the fork's `checkoutReadsRow` reads the authority's `api.lock`                                | planned  |
| zero-trust containment of a worker's own code                                                 | planned  |

### Principles checked

- [Enforcement, not pinky promises](../design/principles/enforcement-not-pinky-promises.md):
  the brief that told a worker not to rebuild its binary failed on 2026-11-12. The pin
  enforces it.
- [Inform people, contain the engine](../design/principles/inform-people-contain-the-engine.md):
  section 1 leaves hostile code to a sandbox, the engine's job, rather than a guard that
  predicts every program a worker could write.
- [Told, never guessed](../design/judgment/told-never-guessed.md): the transport names the
  forker, the store names the authority, and the lease names the row.
- [Verdicts are provenance-blind; disposition is not](../design/principles/verdicts-are-provenance-blind.md):
  a rule's verdict on a line does not change. Which revision may approve depends on who
  calls, and that is disposition, which the page allows.
- [The orchestrator plans, workers work, the integrator lands](../design/principles/the-orchestrator-plans.md):
  section 5 gives the integrator the build, the pin and the advance.
- [Judging is a pure computation (ADR 0039)](0039-judging-is-a-pure-computation.md): the
  authority and the pin are read before judgment. The build-input question runs in the go
  spell when a row is forked or advanced, never per call.
- [One source, every door](../design/principles/one-source-every-door.md) and
  [The host wiring is yours](../design/judgment/the-host-wiring-is-yours.md): section 8.
- [Abstract at the right layer, or not at all](../design/principles/abstract-at-the-right-layer.md):
  one interface, at the rows every lease rule reads. The rules and the verdict are not
  behind an interface.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md) and
  [ADR 0013](0013-the-guard-denies.md): each denial names the row, the authority and the
  remedy, and the remedy lines still pass.
- [Delete before you add](../design/principles/delete-before-you-add.md): the worker relink
  and two Buzz copies of the build-input rule go.

## Alternatives

### Refuse the relink when a worker's diff touches build inputs

This record's first draft. It lost because the relink is one door of several, because the
checkpoint it trusted is client-declared, and because `builds.buzz` already allows the
opposite case. Reopen it if the pin proves unworkable on a host whose hooks cannot check a
hash before delegating.

### One `AuthoritySource` interface for SDK callers

The second draft. It lost because every lease rule reads rows, so an embedder's task got an
authority and none of the lease rules. A row reader serves both.

### A configured list of the judge's paths

A key holding globs. It names one repository's layout, and it is wrong here too: every Go
package linked into `./magus` is part of the judge.

### Keep the checkout's `HEAD` as the authority

Today's behavior. A worker approves its own loosening by committing it.

## Consequences

- A worker never runs a judge built from its own edits, in any workspace.
- A worker that adds a flag tries it through its product build, not `./magus`.
- Every fork records an authority, and the integrator advances it as it lands work.
- A worker running code built to defeat the guard is not stopped until section 9 lands.
- A host that carries no lease for its workers cannot be used for delegated work.

## Open questions

- Which directory a host runs a subagent's hooks in. Section 6 makes the answer matter
  less, but it has not been confirmed at runtime for Claude Code.
- Whether a workspace that installs magus should pin its tool version the same way.

## Amendments
