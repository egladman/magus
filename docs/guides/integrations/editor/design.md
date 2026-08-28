---
title: Event stream design notes
description: Why the magus event stream is outbound only, why it needs no daemon, and what building the first clients changed about the design.
tags: [events, integration, design, decisions, editor, plugin, daemon]
---

# Event stream design notes

Why [the event stream](events.md) is shaped the way it is, and what building the
first clients changed about it. Recorded 2026-08-27.

## The request

Provide a mechanism third parties can build integrations against - an Emacs
package, a Vim plugin, a status bar, a notifier - without magus shipping one
integration per host.

## What the research found

magus does not lack an integration mechanism. It has five partial ones that do
not compose, and no integrator can be pointed at a single thing.

| producer          | schema                                          | transport                                    | consumer today  |
| ----------------- | ----------------------------------------------- | -------------------------------------------- | --------------- |
| `internal/report` | `{"schema":4,"type":"run.target.result",...}` JSONL | stdout of `magus run -o jsonl`           | CI post-process |
| `internal/journal`| `{ts,inv,kind,stream,text,ref}` JSONL           | per-invocation file; loopback SSE with `--open` | browser viewer  |
| `types.Event`     | `{schema_version,outcome,severity,source,where}`| `session notify`, attention store            | humans          |
| `internal/trail`  | Kind + JSONL + blob refs                        | `/api/v1/activity` Connect                   | governance      |
| daemon SSE        | graph seq, base64 proto Status, base64 OTLP     | `/api/v1/events`, bearer-gated               | console PWA     |

An editor plugin can already reach targets (`-o json`), Buzz completion
(`magus buzz lsp`), file changes (`magus watch`), and per-target results
carrying a fetchable `ref`. It cannot reach, without speaking protobuf and
holding a bearer token: live output from a run it did not spawn, workspace-wide
change notifications, or diagnostics.

So the work is not "add hooks". It is "pick one envelope and give it a
subscribe verb". Everything underneath is built.

## Naming

`hook` is taken. `magus session hook` is the agent-host guard adapter: stdin
payload, verdict out. The outbound stream is named `events`, matching the plain
register magus already uses for reads (`refs`, `status`, `watch`, `query`)
rather than the thematic register reserved for engine concepts (spell, charm,
ward).

## The seam test this design has to pass

`docs/scope.md` seals the engine, the cache, the graph schema, and the guard's
evaluation, and states the test for any new extension seam:

> it may change what magus does, never what a verdict means.

An outbound stream passes cleanly: it reads a model magus already built, and no
subscriber can alter an outcome. Inbound lifecycle callbacks - commands magus
invokes mid-run - do not pass, because a post-target callback that touches
outputs breaks the cache-replay contract. They are out of scope, deliberately,
and this section is the record of that decision rather than a gap to fill later.

## Direction: outbound only

magus emits; integrations consume and react. Extending build BEHAVIOR stays
where it already is - spells, charms, the magusfile - which are declarations the
sealed engine evaluates.

## Relationship to `magus session hook`

They are duals, not the same mechanism, and fusing them would open the seam
above.

|            | `session hook`               | `events`                    |
| ---------- | ---------------------------- | --------------------------- |
| direction  | inbound                      | outbound                    |
| shape      | request/reply, blocking      | stream, fire-and-forget     |
| purpose    | change what happens (a verdict) | inform                   |
| audience   | one agent host               | any number of subscribers   |

The unification that is free and correct is at the VOCABULARY, not the
mechanism: the guard already writes `KindAgentCommand` / `KindAgentSpawn` into
the trail, and those become `guard.verdict` events on the stream. `session
notify` becomes `attention.raised`. The inbound mechanism keeps its sealed reply
channel and additionally broadcasts what it decided, so a status bar can show a
denial live without anything being able to influence one.

## Transport: one contract, two transports, one front door

```text
                  types.StreamEvent  (one envelope, one taxonomy)
                           |
                     stdout JSONL
                  magus events --follow
                           |
              <cacheDir>/runs/*.jsonl  (the bus)
```

The transport question answered itself. Every magus run already appends to
`<cacheDir>/runs/<inv>.jsonl`, so a follower polling that DIRECTORY sees runs
from any terminal with no daemon, no socket, and no token. The directory IS the
bus.

`internal/proc` was the candidate before that: a JSONL-framed Unix socket in a
0700 directory where the filesystem permissions ARE the authentication, and
`proc.DiscoverSocket` finds a live daemon with no env var. It lacks a subscribe
frame - every call in `internal/proc/client.go` is request/reply - and adding one
turns a control plane into a bus. That work was not done and is not needed: it
would buy latency over a 250ms poll, and it would make the stream depend on a
daemon the design deliberately does not require.

The CLI stays the front door because the daemon is optional by design
(`docs/scope.md` names the daemon dependency as a standing strain) and because a
subprocess pipe is available in every editor, while a Unix socket is not. There
is no socket frame for this stream and no plan to add one until something needs
the latency: an earlier draft of this section promised integrators "a documented
frame", which was never built.

## The taxonomy

SHIPPED. All four come from the run journal, which is what let the adapter stay a
leaf with no dependency on the engine.

| type            | from                   | why an integration wants it            |
| --------------- | ---------------------- | -------------------------------------- |
| `run.started`   | journal `KindStarted`  | show a spinner, record lineage         |
| `run.finished`  | journal `KindFinished` | clear the spinner, report the outcome  |
| `target.result` | journal `KindResult`   | pass/fail/cached, with a fetchable ref |
| `target.output` | journal `KindOutput`   | live log tailing (opt-in; high volume) |

`target.output` is the one high-volume type and is off unless asked for. An
editor that subscribes to everything by default drowns.

NOT SHIPPED, and deliberately absent from `StreamEventTypes()` rather than
present and silent. Each has a store; none has an adapter. The cost column is
what the review of this branch measured, not an estimate made while designing.

| type                 | store                              | what it costs                                                                                                                |
| -------------------- | ---------------------------------- | ---------------------------------------------------------------------------------------------------------------------------- |
| `diagnostic.emitted` | `diagCollector`, already fanning to report + the graph's runtime shard | smallest: a new `journal.Kind` plus one adapter arm. `file`/`line` need a wider change - `types.DiagnosticEvent` carries only unit, code, message |
| `guard.verdict`      | trail `agent_command`, verdict in a payload blob    | the trail lives in `activity/`, not `runs/`, so this needs a second reader with an unrelated line schema and blob dereferencing. The hook is its own short-lived process, so `inv` would be empty |
| `attention.raised`   | sessions `attention_open`          | a third file, in XDG state rather than the cache dir. The store FLATTENS `types.Event` to strings on the way in, so the body cannot be reconstructed from it without a second record |
| `workspace.changed`  | none - `magus watch` persists nothing | largest, and it breaks the design's central property: there is no store to adapt, so either a long-running watcher writes to `runs/` (which is not what that directory means) or `magus events` grows a watcher and stops being a pure reader of the bus |

An earlier draft of this table listed a `target.started` type sourced from
`KindExec`. It was dropped - `KindExec` is per subprocess, not per target - and
the row outlived the decision by several commits. It is recorded here because a
taxonomy table that describes intentions as facts is the specific way this
document went wrong.

## What a first implementation covers

1. `types/streamevent.go` - the envelope, the taxonomy, the per-type bodies.
   This is the contract, and it is the deliverable that has to be right.
2. `internal/eventstream` - the adapter mapping journal records onto
   `StreamEvent`, plus the cross-process follower over the run-log directory.
   Journal is the ONLY producer adapted; the table above says what the others
   cost. The existing producers keep their on-disk schemas; nothing is rewritten.
3. `magus events` - replay plus `--follow`, `--type`, `--limit`.
4. `internal/proc` - the `events.subscribe` frame and the daemon-side bus.
   NOT built: the run-log directory turned out to serve as the bus without it,
   so this is a latency optimization rather than a requirement.
5. A reference client living beside `docs/guides/integrations/` the way the
   OpenCode plugin does - a template the reader owns and edits, per the "the
   host wiring is yours" entry in `docs/doctrine.md`. Shipped as POSIX sh; see
   [Reference clients](#reference-clients-shell-first) for why, and for what is
   still held back.

## Open questions

- Retention. `magus events` with no `--follow` replays recent events; from
  where? The journal store is per-invocation and already rotated; the trail caps
  at 10000 and rotates on a schedule. A replay window that spans producers needs
  one answer, and inventing a sixth durable log to get it would be the wrong one.
- `target.started` was dropped: journal emits `KindExec` per subprocess, not per
  target, so there was no producer to adapt. A consumer that wants per-target
  progress infers start from the first `target.output`. Reopen this only if the
  engine grows a per-target start event for its own reasons.
- Windows. `SockDir` has a Windows variant but the socket bus needs checking
  there; the CLI front door is unaffected.

## Decision: the daemon is an accelerant, never a capability gate

Recorded 2026-08-27, after the transport question surfaced a wider one.

The complaint that prompted it: some capabilities are daemon-only and some are
not, which is hard to support and impossible to document without a fork in every
paragraph. The proposed fix was to auto-start the daemon on any command.

That was rejected, and a narrower rule adopted instead:

> No capability is daemon-only. The daemon is only ever an accelerant.

### Why not global auto-start

- It trades a legible failure for an illegible one. "Daemon off" is visible today
  and one command fixes it. A daemon that fails to launch, wedges, or is a stale
  binary serving a newer CLI turns EVERY command into a hang with no obvious
  cause - the same failure class MGS1021's stale-binary explainer exists for.
- `magus ls` inside a `docker build` layer would leave an orphaned background
  process. A build tool that silently spawns long-lived processes is a surprise.
- `docs/scope.md` promises the daemon "carries an asterisk". Global auto-start
  makes it a de facto runtime requirement, which is a doctrine change rather than
  a feature, and it should be made deliberately if it is made at all.

### Why the invariant fixes the documentation

Documentation forks because capabilities fork, not because the daemon is
sometimes down. With the invariant, no page ever says "if the daemon is running,
X; otherwise Y". It says X, and the daemon makes X faster.

### The residual set, and why it is not a violation

`/mcp` and the console are network surfaces BY DEFINITION - something connects to
them over a socket. The shared concurrency pool and background jobs are
cross-process by definition. Nobody is surprised that asking for a server needs a
server, so these are not a capability split; they are the daemon's own surface.

For those, a command SHOULD auto-start the daemon, because asking for the console
IS asking for the daemon and starting it is doing what was asked rather than a
side effect. CI never spawns one under this rule - not by a special case, but
because CI never asks for a console. That absence of a conditional is the point.

`graph export --open --follow` does this, via `ensureConsoleDaemon`. A first
implementation was reverted before merge and rebuilt, and the three failures that
review found are the specification for anyone touching it again:

- **The child must not inherit `MAGUS_DAEMON_SOCKET`.** `spawnDetachedDaemon` was
  safe only because its one caller ran under a dispatch profile that never hosts a
  per-process proc server. Called from one that does, the child decided it was
  already adopted, bound no socket, and reported its PARENT's - leaving a daemon
  `magus server stop` could not find and only `kill` could remove.
  `daemonChildEnv` scrubs it.
- **Every failure path must reap what it spawned.** With `console.enabled=false`
  the wait times out by construction, so each attempt leaked one more process.
  `reapDaemon` kills it on both the timeout and the cancellation path.
- **It must say which tree it came up in.** One socket per user serves every
  workspace, so a daemon started here is authoritative for whoever connects next.
  This is the same hazard `servingSuffix` exists for on the `server start` path.

`--print` is exempt: the scriptable "just give me the URL" form must not leave a
background process behind.

Still open: promoting one worktree's binary to a long-lived per-user service is
what the checkout guard exists to prevent, by a route the guard cannot see
because there is no `cd` in the command line.

### This was already the de facto rule

Two sites decided it independently before it was stated:

- `internal/doctor/checks_mcp.go` degrades the console check when no daemon is
  running, reasoning that "a check that is red by default is a check people learn
  to ignore, taking the real failures with it".
- `cmd/magus/graph.go` refuses `--follow` with `clihint.ServerStart` rather than
  starting one, under a comment reading "magus never auto-starts a daemon".

The second is the site this decision REVERSES: `--follow` is a plain request for
the console, so it starts the daemon rather than refusing. The first stays as it
is and becomes the worked example of the rule.

### The event stream needs none of this

`magus events` reads the run-log directory, which every magus process already
writes to. It has no daemon dependency and no tier split, and this decision does
not change it. A daemon-side push would lower latency below the poll interval and
is an optimization, not a second implementation.

### How the invariant is enforced

A rule that lives only in prose is a rule with roughly even odds (CLAUDE.md says
so, and measured it). It needs a test in the shape of
`TestNoHostSpecificBehaviorInCode`: a gate that fails when a command's failure
path reports a capability as unavailable because no daemon is running, rather
than degrading or starting one.

## What the implementation changed about the design

Five things only showed up once real events flowed. Each is recorded because the
design as written would have shipped wrong without it.

**The run-log directory is the bus.** Every magus RUN already appends to
`<cacheDir>/runs/<inv>.jsonl`, so a follower reading that directory sees runs
from any terminal with no daemon, no socket, and no token. Only runs: commands
that open no invocation write no log and produce no events. Verified end to end: a
follower process picked up runs started by a separate process, live. This demotes
the `proc` `events.subscribe` frame from REQUIRED to a latency optimization, and
it is what lets the stream satisfy the daemon invariant above rather than
violating it.

**The journal buffered, so a naive tail could not fire.** `journal.FileHandler`
wrote into a `bufio.Writer` flushed only at run end, so a follower would have
lagged by up to a page and a short run would have delivered nothing until it
finished - shipped, green, and unable to work. It now flushes every kind EXCEPT
output: lifecycle and result are one per run and one per target, so the syscall
is free at that rate, while output stays buffered as the one high-volume kind.
That is a hot path changed for a new feature's benefit and deserves review.

**jsonv2 does not omit zero numbers.** The repo builds with the jsonv2 codec,
where `omitempty` omits only empty JSON values (null, "", [], {}) and NOT `0`.
Every `duration_ms,omitempty` therefore shipped `"duration_ms":0` on events where
the field does not apply - telling a subscriber a cached replay took no time
rather than that it never ran. The numeric fields carry `omitzero`, and a test
pins the distinction. Any new numeric field on this contract has the same trap.

**"Is a daemon running" cannot be answered by looking at a socket.** A magus
invocation hosts its own proc server on the stable socket AND exports
`MAGUS_DAEMON_SOCKET` into its own environment so children inherit adoption. Both
of the obvious checks therefore report a process as being served by itself, and
the warm-graph hint never fired. The question is whether the PID on the other end
is somebody else. Three implementations were wrong before the fourth worked; the
comment on `servedByAnotherDaemon` records why.

**A relative `--root` shipped a relative workspace.** The contract promises an
absolute root because a subscriber watching two workspaces routes on it, and a
relative path resolves against the SUBSCRIBER's cwd rather than the producer's.
`magus events` absolutizes.

## `--limit 0` means replay NOTHING

Found by running the shell watcher: it announced the previous day's failure the
moment it started. `--limit` originally read 0 as "replay everything", so there
was no way to ask for "only what happens next" - `Follower.Skip` existed in the
library and the CLI could not reach it.

The semantics are now the ones a subscriber actually needs:

| `--limit` | on attach                              |
| --------- | -------------------------------------- |
| `0`       | nothing; only what happens from now on |
| `N > 0`   | the last N invocations, then follow    |
| `< 0`     | every retained invocation, then follow |

A notifier wants 0; a statusline wants 1. The library's `Replay` still reads 0
as "no cap", and the CLI maps - changing the method would have altered what it
means for every other caller to fix a flag's ergonomics.

## Reference clients: shell first

The first draft led with an editor package. That was the wrong shape, and using
the thing is what showed it.

The repo's existing reference integrations (`docs/guides/integrations/agents/*.sh`)
are POSIX sh for a reason doctrine states: the contract should be small enough to
hold in your head. An editor package buries the ten lines that teach the contract
under a couple of hundred lines of mode, hook, and buffer plumbing.
`magus-events-watch.sh` is the whole contract, and an editor client is visibly
that loop plus a way to draw on a screen.

Editor clients for Vim and Emacs are drafted but deliberately not shipped yet:
they are the part of this work that most needs a human to read it, and an
unreviewed plugin in the docs tree is a promise magus has not checked.

Two findings from writing them are worth keeping even so:

- **Vimscript reaches more editors than Lua and is shorter here.** Nothing in
  this contract needs Neovim - it is a subprocess pipe, and Vim 8 has had
  `job_start()` and `json_decode()` for years. The deciding detail runs opposite
  to the usual assumption: Vim's `out_mode: 'nl'` delivers exactly one complete
  line per callback, so the Vim path needs no line buffering, while Neovim's
  `on_stdout` hands over a list whose last element may be partial.
- **VS Code is the widest audience and the worst reference.** Its extension
  scaffolding (package.json, tsconfig, a bundler) dwarfs the fifteen lines that
  matter, and `readline.createInterface` hides the partial-line problem rather
  than teaching it. `docs/scope.md` already names the OpenCode plugin's upkeep as
  more than an example should need.
