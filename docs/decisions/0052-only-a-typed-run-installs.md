---
title: "ADR 0052: only a typed run installs"
order: 52
description: A project's dependencies are installed only inside a run somebody typed. When such a run freshens a symbol index, magus runs the indexed project's install first, through that run, and the install's own cache step decides whether it does anything. A read, a background index run and the watcher install nothing; a symbol index they cannot make current over a tree missing its install fails MGS7003 naming `magus run install <project>`. No charm grants an install, and the edge from an index to its project's install lives below the magusfile.
tags: [adr, decision, install, symbols, network, consent]
status: accepted
date: 2026-11-17
---

# ADR 0052: only a typed run installs

## Context

CI shard 1e of pull request 775 (run 44096871649, job 134097090807) failed in docs'
`site-generate`. The cause, at log line 1496: "tsc is not installed, so its cache key
records UNPROBED: no node_modules in docs/guides/integrations/agents: run `magus run
install docs/guides/integrations/agents`", followed by MGS7003 for that project.

- `site_generate` needs root `symbol_digest_generate`, which reads `magus\graph()`
  unscoped (`magusfile.buzz`) and so freshens every project's symbol index
  (`freshenSymbolIndexes`, `symbol.go`).
- The freshen probes every tool of an indexed project, tsc included. tsc's probe is
  `pnpm exec tsc --version` and reads `node_modules` (`probeInputs`, `probe.go`).
- The agents index had been a cache hit. A biome reformat of `cursor-schemas.ts` moved
  its key, and the shard had never installed that project, because nothing the shard ran
  reaches `docs/guides/integrations/agents:install`.
- A magusfile cannot declare the missing edge. docs imports the root
  (`ctx.needs(workspace\symbol_digest_generate)`, `docs/magusfile.buzz`), so the root
  cannot `ctx.needs(docs\install)`, and the index op is synthesized from the spell's
  `mgs_getSymbolIndexer`, which declares no needs (`addSymbolIndexOp`,
  `internal/spell/decode.go`). The edge was prose in a bootstrap comment
  (`hack/dev/bootstrap-checkout.buzz`: "a fresh checkout has no node_modules, and the
  TypeScript symbol index needs tsc").
- The fix sentence existed (`missingInstall`, `install.go`) but reached the person only
  as a warning above an MGS7003 that did not carry it.

Promises an install must keep:

- [ADR 0014](0014-running-magus-is-the-consent.md): "magus instead never runs the
  repository's code passively: only a command you typed, or a tool call that names the
  workspace, does." A read verb that writes `node_modules` is the shape of its P1.
- `cmd/magus/diff.go`: "a plain report never reaches the network". The MCP diff tool, the
  console diff handler and change risk freshen indexes through the same function.

## Decision

1. A project's install runs only inside a run somebody typed. When such a run freshens a
   symbol index, magus first dispatches each indexed project's install through that run,
   as a cross-project `ctx.needs` does (`installIndexedProjects`, `symbol.go`). It
   dispatches the project's own `install` target when the project has one, so a
   `ctx.needs(install)` in the same run meets the same node, else the spell's install op.
2. Whether an install has anything to do is its own cache step's answer: it replays when
   the manifest, lock and stamps are unchanged, so a moved lockfile reinstalls. magus
   keeps no second definition of "installed" to decide whether to dispatch it. An install
   into a global store (Go's `go-mod-download`, Dir empty) is not dispatched: it puts
   nothing in the tree an index reads.
3. A read installs nothing: `magus query`, `explain`, `refs`, `diff`, the MCP diff tool,
   the console diff handler, change risk, and a Buzz `magus\graph()` outside a run. Nor
   do a background index run (`run{Background: true}`), the watcher, or a read's rebuild
   under its ancestor's lock (`run{UnderAncestor: true}`). The run records whether it is
   typed on its context (`run.typed`, `withTypedRun`, `run.go`), afresh for each run.
4. An index that a read cannot make current over a tree missing its install fails
   MGS7003, and its problem line names what is missing and the command:
   `docs/guides/integrations/agents: its dependencies are not installed: no node_modules
   in docs/guides/integrations/agents: run magus run install
   docs/guides/integrations/agents` (`installGap`, `install.go`). magus builds no index
   over that tree.
5. No charm grants an install. The run verb is the consent, and `update` stays the only
   charm that moves pins ([charms](../concepts/charms.md)).
6. The edge from an index to its project's install lives in the engine, below the
   magusfile. Both ops are synthesized by magus, the probe table already knows tsc lives
   in the install tree, and the import cycle keeps the edge out of any magusfile.

| Item                                                                            | State     |
| ------------------------------------------------------------------------------- | --------- |
| Typed-run freshen dispatches each indexed project's install first               | done      |
| Read, background and under-ancestor freshens install nothing                    | done      |
| MGS7003 names `magus run install <project>` for a tree missing its install      | done      |
| Tests pinning all three (`TestFreshen*Install*`, `symbol_test.go`)              | done      |
| A behavior test that `diff`, `query`, `explain`, `refs` fork no package manager | planned   |
| `MAGUS_OFFLINE` reaching installs                                               | not built |

### The background-run trap

The rule keys on "a run somebody typed", not on "a run". A plain `magus query` starts a
detached index refresh (`RefreshSymbolIndexes`, `symbol.go`, `run{Background: true}`), and
the watcher runs index ops too. Both are runs. If the install edge keyed on being inside
any run, the next time it moves from the freshen into the op graph a read would install
in the background, where nobody sees the network call. `run.typed` excludes Background
and UnderAncestor runs, and `TestFreshenInsideABackgroundRunInstallsNothing` pins it.

### Principles checked

- [ADR 0014](0014-running-magus-is-the-consent.md): kept. Only a typed command or a run it
  started installs.
- [No silent defaults](../design/judgment/no-silent-defaults.md): the install command
  moves from a warning into the returned MGS7003.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md): the
  refusal names the missing directory or stamp and the one command that fixes it.
- [A cache is an accelerator](../design/principles/a-cache-is-an-accelerator.md): the
  index stops depending on a cache hit to avoid a cold tree.
- [Told, never guessed](../design/judgment/told-never-guessed.md): magus does not detect
  CI to decide whether to install.
- [One source, every door](../design/principles/one-source-every-door.md): the rule lives
  once, under the CLI, a magusfile, a script and MCP alike.
- [Friction that carries meaning](../design/principles/friction-that-carries-meaning.md):
  an install charm would be typed by every fresh clone and say nothing.
- [Enforcement, not pinky promises](../design/principles/enforcement-not-pinky-promises.md):
  the edge moves from a bootstrap comment into the engine, with tests.

## Alternatives

### Install wherever a freshen finds a cold project

The first fix (commit d00b07a41, held) ran the install before the probe in every freshen:
through the run inside a target body, as its own invocation anywhere else.

- Why it lost: `magus diff`, the MCP diff tool, the console, change risk and any Buzz
  graph read would fork a package manager and write `node_modules` into a checkout the
  caller only read. That breaks ADR 0014 and the `cmd/magus/diff.go` promise above.
- It also decided "cold" by a second, weaker test: the directory and stamps exist. A tree
  whose lockfile moved after a rebase passed that test, so the index was built against
  stale dependencies. The install step's own key sees the moved lock
  (`installStep`, `install.go`).
- What would reopen it: a read verb whose contract says it may reach the network, typed
  as such.

### A canonical install magus wires in, with network only under a charm

Two parts: magus derives "this op needs its spell's install" for every op, and a plain run
refuses a cold tree unless it carries an `:install` charm.

- Why the wiring lost, for now: keyed on the spell, a formatter from PATH pays an install;
  keyed on the probed tools in the tree (`execs`, `probe.go`), an op that imports from
  `node_modules` with no probed binary is missed. docs routes every build target through
  one `install` so parallel installs do not race (`docs/magusfile.buzz`), which a blanket
  edge would bypass.
- Why the charm lost: every fresh clone and every dependency bump ends in the same
  refusal, so the charm is typed every time and carries no meaning. It also cannot say
  whether the install fetches: a warm pnpm store is a link step, a cold one a download
  (`--prefer-offline`, `spells/typescript/spell.buzz`). `update` marks bytes that differ
  by day; a frozen install gives the same bytes every day.
- What would reopen it: a measurement of how often a missing `ctx.needs(install)` bites
  ordinary ops, for the wiring; a per-install offline form, for a network disclosure.

### Install in CI only

A workflow step runs `magus run install --all`, or the affected projects, ahead of each shard,
as the bootstrap does locally (`hack/dev/bootstrap-checkout.buzz`).

- Why it lost: an unscoped graph read freshens every project, affected or not, and the
  agents project was in neither the affected set nor the declared needs. "Install the
  affected projects" misses the next cold project; "install everything" charges every
  shard for every project. The rule would live in a workflow file, so a consumer
  workspace with none inherits the failure.
- What would reopen it: none while the engine holds the edge; a CI install step may still
  be a speed choice.

## Consequences

- A typed run that reads the graph may now take an install step for a project it never
  named, such as `magus run symbol-digest-generate .` installing every indexed project that
  is behind. The install shows as its own step and replays when nothing moved.
- A read in a fresh clone, or after deleting `node_modules`, fails MGS7003 with the
  install command, where it used to build an index over the cold tree or fail with only a
  warning.
- An unscoped `magus\graph()` installs projects a Go-only reader never asked about;
  `FreshenSymbolIndexes` takes `languages` for a caller that can scope.

## Open questions

- Whether `magus graph build`, a typed verb that runs index ops, should follow the install
  edge or keep refusing as a read does. It refuses today.
- Whether `MAGUS_OFFLINE` should reach installs, with each `Install` declaring its offline
  form (pnpm `--offline`), so a cold store fails by name instead of fetching.
- Whether `magus describe target` and `--dry-run` should name the install steps a target
  reaches, so "will this fork a package manager" has an answer before the run.
