---
title: "ADR 0028: the mod draws magus figures it does not compute"
order: 28
description: >-
  The Claude Code mod gets three inline views: the job plan an agent forks, a live run of magus run or magus affected, and the shape of a magus query and its answer. Each figure is composed in Buzz with magus/figure and drawn by libs/figure, from data core already serves or will journal. The mod's TypeScript only notices the call, runs the Buzz script through magus, and places the SVG. A mod cannot load WebAssembly, so the OpenCode wasm shim does not carry over; a process per frame does.
tags: [adr, decision, agents, claude-code, mod, figure, buzz]
status: proposed
date: 2026-11-07
---

# ADR 0028: the mod draws magus figures it does not compute

## Context

A person watching an agent sees `magus job fork`, `magus run`, `magus affected` and
`magus query` only as text in a collapsed tool row. The fan-out an agent planned, which
target a run waits on, and what a query matched are each one command away, and nobody
runs that command mid-session.

What exists on main at `d29cea148`:

- `magus job graph -o svg` writes the live job plan through `job.NewPlan(...).Figures()`
  and libs/figure. Nothing in the mod shows it.
- The terminal run band draws a live wait tree from `cache.plan` and `cache.step` records
  (#694). Those records are band-only: the run journal (`proto/magus/viewer/v1alpha1`)
  holds `KIND_EXEC` and `KIND_RESULT` per target, and no plan, edges or start.
- `ViewerService/StreamEvents` replays and follows a run's journal.
- `magus query -o json` carries the raw `query` string, each match's `why.terms` with the
  field a term scored on, and the neighborhood's `nodes` and `links`. It does not carry the
  parsed filters (`kind=`, `id=~`, `!=`).
- Buzz composes figures with `magus/figure` outside the docs site:
  `hack/dev/show-session-figure.buzz` draws a box per job and an edge per spawn. The
  console runs the same figures in wasm (`console/src/apps/graph/diagrams/wasm.ts`).
- The mod (`docs/guides/integrations/agents/claude-code-mod`) is TSX, 948 lines in
  `docs/guides/integrations/agents/claude-code-mod/hooks/register.tsx`. Its environment has no `WebAssembly`, no `eval` and no Node; the
  engine types say compiled code "runs it in a process of its own through `$.process.run`".
- A mod draws `Svg` on desktop, vscode and mobile, with SMIL and CSS animation and hover
  under `isInteractive`, up to 131,072 characters. The terminal has no `Svg`.

## Decision

The mod draws three figures. It computes none of them.

| Layer | Owns | Language |
| --- | --- | --- |
| core | the data: job rows, the run journal, the query record; the figure library | Go |
| figure scripts, in the `claude-code-mod` spell | which boxes, edges, zones and states each view draws | Buzz, `magus/figure` |
| mod | noticing the call, running the script, placing the SVG, the terminal fallback | TSX |

### The workspace owns the figures

The figure scripts are part of the `claude-code-mod` harness spell
(`spells/harness/claude-code-mod/figures/<view>.buzz`), not the Go source and not the
plugin folder. A workspace that wants other figures copies the spell into its tree and
declares the copy as a `path:` override of the registry path in `magus.yaml`, the
ownership switch [Agents](../guides/integrations/AGENTS.md#set-up-your-host) already
documents for a shipped harness. Its agents and people edit that Buzz and see the change
on the next frame, with no magus release and no plugin update. The spell exports
`mgs_figures()`, a map from view to script path. The mod reads that map through
`magus describe harness claude-code-mod -o json`, so it never names a path itself.

### Three parts, three update paths

Each part changes for a different reason, so each ships on its own path. No part waits
on another's release.

| Part | Changes when | Ships as | Updated by | State |
| --- | --- | --- | --- | --- |
| mod (TSX) | Claude Code's plugin API changes | the marketplace plugin at `.claude-plugin/marketplace.json`, from this repository's default branch | `claude plugin update`; no magus release | done (as the mod ships today) |
| figure scripts (Buzz) | a person wants another figure | the `claude-code-mod` spell, pinned per workspace in `magus.lock` | the workspace's spell pin, or its own `path:` override | proposed |
| data and drawing (Go) | a record or the figure library changes | the magus binary | a magus release | done (as magus ships today) |

The mod talks to magus only through surfaces magus already versions: CLI records
(`-o json`, `-o svg`), the Connect `v1alpha1` services, and `magus describe harness -o json`.
Nothing of the mod is embedded in the binary, and nothing of magus is copied into the
mod. When a workspace's magus lacks a surface a view needs, the view draws one red line
naming the magus version that has it. It never draws a guess.

### The three views

| View | Invocation command | Data | State |
| --- | --- | --- | --- |
| job plan | `job fork`, `job graph`; also the pane's Plan tab | `magus job graph -o svg`, as built | proposed |
| live run | `run`, `affected` | the run's journal, read by `magus query invocation <id> -o json` | proposed |
| query | `query`, `explain`, `refs` | the invocation's query record | proposed |

A view is chosen by the command of the invocation a call started, never by the call's
text. A Bash call running `./magus` and a magus MCP tool call (`client`, `buzz`, `status`,
`console`, `config`, `diff`) reach the same code path and leave the same invocation, so
they get the same figure. The mod never parses a shell line or the Buzz a `client` call
carries.

### What core adds

| Change | Why | State |
| --- | --- | --- |
| journal `cache.plan` as `KIND_PLAN` and `cache.step` as `KIND_STEP` (`v1alpha1`, additive) | a run figure needs the planned targets, their edges and when each started | proposed |
| a `parsed` field on the query record: each filter's field, operator and value, and the free terms | the parser stays the one in Go; a script never splits a query string | proposed |
| a running state in libs/figure that animates (a SMIL pulse on the box) | every figure, console included, gets the same running mark | proposed |
| every MCP tool call journals an invocation with its command and the tool call's id, as a CLI call does | the mod finds a call's invocation the same way for both doors | proposed (not yet checked for each tool) |

### What the mod adds

- One `ui.render` hook on `ToolUse` for a Bash call that runs magus and for every
  `mcp__magus__*` call. It finds the invocations the call started in `ListInvocations`,
  picks the view from each one's command, and draws the engine's own row, then the figure.
- The figure comes from one `$.process.run` of `./magus buzz <script>`, where the script is
  the one `mgs_figures()` names for the view, called with the call's identity (job id, invocation id, query text) as arguments. The script
  prints the SVG and a one-line `alt`.
- A live run redraws once a second until `KIND_FINISHED`, one process per frame, never
  two in flight.
- On the terminal, the same script with `-o text` prints the tree the band already shows.

### Principles checked

- [One source, every door](../design/principles/one-source-every-door.md): a figure is a
  Buzz script over core's data, so the console can run the same script in wasm. The mod
  gets no second layout.
- [Abstract at the right layer](../design/principles/abstract-at-the-right-layer.md):
  core serves facts, Buzz composes, the mod places. No layer renames another's call.
- [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
  the figures draw for the person and reach no model. The mod changes no tool input,
  output or prompt. This decision depends on it.
- [No news is good news](../design/principles/no-news-is-good-news.md): a cached run
  draws no live figure. Like the band, it waits 3 seconds before drawing.
- [No silent defaults](../design/judgment/no-silent-defaults.md): if no daemon is running,
  the journal is unreadable or the script fails, the figure draws one red line with the
  command to run. It never draws an empty box.
- [Human first is the AI integration](../design/principles/human-first-is-the-ai-integration.md):
  every figure is also `magus buzz <script>` for a person at a shell.

## Alternatives

### Compute and draw the figures in TSX

Rejected. It would be a second layout beside libs/figure, a second query parser, and a
third copy of the job tree (the mod's
`docs/guides/integrations/agents/claude-code-mod/hooks/tree.ts` is already one). The
console could not reuse it. A workspace could change it only by forking the plugin. Reopen
it if a frame must update faster than a process spawn allows. No such frame has been
measured.

### Embed the figure scripts in the binary

Rejected. That gives one copy, but a workspace can't change it short of a magus release.
That fails the reason the figures are Buzz in the first place. Reopen it if a figure must
hold for every workspace unchanged, as a guard verdict does.

### Embed the whole mod in the binary and install it with `magus agent install`

Rejected. Each Claude Code plugin API change would then need a magus release, so the
binary would move in lockstep with Claude Code. The marketplace already updates the mod
without one. Reopen it if Claude Code drops marketplace installs.

### Ship the figure scripts in the plugin folder

Rejected. The plugin is installed per person into Claude Code's plugin cache. An edit
there is lost on the next plugin update, and no one else on the repository sees it. A
spell is versioned with the workspace, pinned in `magus.lock`, and overridden by a path
the repository commits.

### Load the Go or Buzz runtime as wasm, as the OpenCode spell does

Not possible. The mod environment has no `WebAssembly`, by the engine's design. Reopen it
if the engine adds it.

### A `-o svg` on each command, with no Buzz

Set aside. `job graph -o svg` already works this way, and the plan view uses it as built.
For the run and query views, a Go figure per command would put composition in core, and
core holds primitives, and a workspace could not change them. Reopen it if one figure
frame is measured above 250 ms.

### Stream `StreamEvents` into the mod

Set aside for now. `$.http.fetch` answers whole bodies, and a frame needs the figure
re-composed anyway. A one-second poll through the script costs one process per frame.
Reopen it if the engine gains a streaming fetch and the poll proves too slow.

## Consequences

- Desktop gets figures. The terminal gets the text tree, because it has no `Svg`.
- The journal grows by one `KIND_PLAN` event per run and one `KIND_STEP` event per step.
  Older readers skip unknown kinds.
- The figure scripts run on the workspace's `./magus`, from the spell the workspace pins
  or overrides. A `./magus` older than the spell fails the script, and the mod's health
  check already names the rebuild.
- A workspace that overrides the spell owns its figures from then on. A later magus
  release does not reach them until it merges the shipped spell's changes, as with any
  adapted harness.
- A `magus run` the agent starts with no daemon draws only the red line.

## Open questions

- The marketplace serves the mod from the default branch, so a workspace pinned to an
  older magus gets the newest mod. A versioned marketplace ref per magus release would
  let it pin the mod too. The red line covers the gap until then.
- Whether the remote spell registry publishes `claude-code-mod` apart from a binary
  release. If it doesn't, a figure change rides the next release.
- Whether the console runs the workspace's spell copy of a figure or its own. One source
  says the spell's, but the console's wasm build has no spell path today.
- How a Bash call's invocation is tied to the call. An MCP call can carry its tool call id.
  A shell line can't, so the choice is between a time window on `ListInvocations` and an
  environment variable the guard sets on the call.
- Which MCP tools journal an invocation today. A `client` call that answers from the
  daemon's warm graph may leave none.
- Not measured yet: one `magus buzz` figure frame on a 50-target run.

## Amendments
