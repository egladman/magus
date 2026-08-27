---
title: The event stream
description: Subscribe to magus events as JSONL with magus events, the contract editor plugins and other integrations build against.
tags: [events, integration, editor, plugin, emacs, vim, jsonl, subscribe, stream]
aliases: [guides/events]
---

# The event stream

`magus events` emits one JSON object per line describing what is happening in a
workspace. It is the surface an integration builds against: an editor plugin, a
status bar, a notifier, a dashboard.

```sh
magus events --follow
```

Every magus process in the workspace feeds the stream, so a run started in
another terminal shows up here. It needs no daemon, no token, and no loadable
magusfile - an editor can attach to a repository whose magusfile is mid-edit,
which is exactly when someone wants it.

## The line shape

Envelope fields and the event's own fields sit at one level:

```json
{"schema":1,"type":"target.result","ts":1756312800000,"workspace":"/repo","inv":"inv1a2b3c","project":"cmd/magus","target":"build","status":"failed","cache_hit":false,"ref":"out_abc","duration_ms":1200}
```

| field       | meaning                                                          |
| ----------- | ---------------------------------------------------------------- |
| `schema`    | envelope version; see [Compatibility](#compatibility)            |
| `type`      | what kind of fact this is                                        |
| `ts`        | unix milliseconds                                                |
| `workspace` | absolute repository root, so a subscriber can watch more than one |
| `inv`       | groups every event of one invocation; absent outside a run       |

## The types

| type                 | when                          | key fields                                     |
| -------------------- | ----------------------------- | ---------------------------------------------- |
| `run.started`        | an invocation opens           | `command`, `trigger`, `magus_version`          |
| `run.finished`       | it closes                     | `status`, `duration_ms`                        |
| `target.result`      | one target finishes           | `project`, `target`, `status`, `cache_hit`, `ref` |
| `target.output`      | a subprocess writes a line    | `stream`, `text` - opt-in, see below           |
| `diagnostic.emitted` | a coded finding               | `code`, `message`, `file`, `line`              |
| `workspace.changed`  | source files move on disk     | `paths`                                        |
| `attention.raised`   | something is blocked on a person | `id`, `request`                             |
| `guard.verdict`      | the guard judged a command    | `verdict`, `surface`, `subject`, `reason`      |

### status and cache_hit are separate questions

`status` says whether the target SUCCEEDED. `cache_hit` says whether it actually
RAN. A replay is `{"status":"ok","cache_hit":true}`. Rendering a replay as a
fresh build is the common misreading, so the two axes are kept apart rather than
folded into one three-valued field.

### target.output is off by default

It is the one type that scales with build size rather than project count: a busy
`affected ci` emits tens of thousands. Ask for it explicitly:

```sh
magus events --follow --type target.output
```

Usually you do not want to. A `target.result` carries `ref`, the address of that
execution's captured log, so a subscriber fetches the one log it cares about:

```sh
magus query output out_abc
```

## Filtering

```sh
magus events --follow --type target.result,diagnostic.emitted
```

An unknown type name is an error, not an empty match. A typo would otherwise
present as a workspace where nothing ever happens, which is indistinguishable
from a broken integration.

## Replay

Without `--follow`, `magus events` prints recent history and exits. `--limit`
bounds how many invocations it reaches back through; `--limit 1` is "what did the
last run do".

```sh
magus events --limit 1
```

`--limit` also decides what a `--follow` subscriber sees on attach:

| `--limit` | on attach                                    |
| --------- | -------------------------------------------- |
| `0`       | nothing; only what happens from now on       |
| `N > 0`   | the last N invocations, then follow          |
| `< 0`     | every retained invocation, then follow       |

A notifier wants `0`. A statusline wants `1`, so it has something to show before
the next run starts.

## Compatibility

**A subscriber must ignore types and fields it does not recognize.** That rule is
what lets magus add an event type without breaking clients built against an
earlier release, and it is the only thing asked of an integration.

`schema` is bumped when a field is renamed or removed, or when an existing field
changes meaning. Adding a type, or adding an optional field to an existing type,
is additive and does not bump it.

## What the stream is not

It is outbound only, and there is no reply channel. Nothing a subscriber does can
change what magus decides - the engine, the cache, the graph schema, and the
guard's evaluation are sealed (see [scope](../../../scope.md)), and an extension
seam "may change what magus does, never what a verdict means".

The inbound counterpart is [`magus session hook`](../agents/guard.md), which is
request/reply and returns a verdict. A guard denial reaches this stream as a
`guard.verdict` event AFTER the fact, so a status bar can show it and nothing
can influence it.

To change what a build DOES, the extension points are the ones that already
exist: spells, charms, and the magusfile - declarations the sealed engine
evaluates, auditable in a diff.

## Latency

`--follow` polls the run log. Lifecycle, result, and diagnostic events are
flushed as they happen; `target.output` is buffered and arrives in chunks, or at
the end of the run. Tune the poll with `--interval`.

## A reference client

`magus-events-watch.sh`, in
[`docs/guides/integrations/editor/`](https://github.com/egladman/magus/tree/main/docs/guides/integrations/editor),
prints every target failure as it happens, anywhere in the workspace:

```sh
./magus-events-watch.sh
```

It is a TEMPLATE you own, not a package magus releases - the same arrangement as
the [agent guard templates](../agents/guard-templates.md), and for the same
reason ("The host wiring is yours" in [doctrine](../../../doctrine.md)).

POSIX sh and jq, nothing else. The interesting part is that there is no
interesting part:

```sh
magus events --follow --limit 0 --type target.result | jq -r --unbuffered '
  select(.status == "failed") | "FAIL \(.project):\(.target)"
'
```

`--limit 0` replays nothing, which is what a notifier wants: waking up and
announcing yesterday's failure is worse than staying quiet.

That loop is the whole contract. An editor plugin is the same thing plus a way
to draw on a screen.


## Writing your own

Spawn `magus events --follow`, buffer partial lines, decode each complete one,
switch on `type`. That is the entire client.

The partial-line handling is the one thing worth getting right rather than
discovering. A process filter or job callback is handed arbitrary BYTES, not
lines, so a JSON object routinely arrives split across two callbacks. Hold
whatever follows the last newline until the rest of it turns up.

Two runtimes do it for you, and it is worth knowing which:

- **Vim**: `job_start()` with `out_mode: 'nl'` delivers exactly one complete
  line per callback.
- **Node / VS Code**: `child_process.spawn` plus `readline.createInterface`.

Everything else needs the pending buffer - Neovim's `on_stdout` hands over a
list whose last element may be partial, and an Emacs process filter gets raw
chunks.

For editing magusfiles and spells - completion, hover, signature help - see
[Editor setup](../editor.md), which wires `magus buzz lsp`. The two are
independent: the language server is edit time, this stream is run time.


## See also

- [design.md](design.md): why the stream is shaped this way - outbound only, no
  daemon, and what building the first clients changed about it.
- [editor.md](../editor.md): the language server for `*.buzz` files.
- [daemon.md](../daemon.md): the daemon, and why this stream does not need one.
- [console.md](../../../reference/console.md): the browser surface reading the
  same underlying state.

## Stopping a follower

`--follow` runs until you stop it. A signal ends it the way it ends any
long-running magus command: the process exits `128+N` (143 on `SIGTERM`, 130 on
`Ctrl-C`), which is the shell convention and NOT a crash. A plugin's process
sentinel should treat those two as a normal stop.

## Known limits

- **Poll cost scales with retained runs.** `--follow` re-reads the run-log
  directory every `--interval`, and each poll stats the logs it finds. Those logs
  are pruned by a daemon maintenance job, so a workspace running without a daemon
  accumulates them. On a busy repo left running for weeks, raise `--interval` or
  start a daemon. This has not been measured; the shape of the cost is stated so
  you can recognize it rather than as a claim about when it bites.
- **`target.output` arrives in chunks.** Only that type is buffered (see
  [Latency](#latency)), so a subscriber to it sees bursts rather than a smooth
  tail. Fetch a finished target's log by `ref` when you want it whole.
