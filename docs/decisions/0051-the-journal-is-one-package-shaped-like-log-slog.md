---
title: "ADR 0051: the journal is one package shaped like log/slog"
order: 51
description: "The record of what happened is one concept, the journal, and the word trail leaves the code. libs/safetyscissors/journal holds two line types and their readers: Record, one line of an invocation's journal, a concrete struct with a Kind and one accessor per kind; and Call, one line of the activity log. journal/stream is the subscriber view. Both writers stay in magus's internal/recorder behind a Sink function until the guard moves into the module. Nothing on disk changes."
tags: [adr, decision, sdk, journal, harness, naming]
status: proposed
date: 2026-11-16
---

# ADR 0051: the journal is one package shaped like log/slog

## Context

magus keeps two append-only records and names them four ways. An invocation's events are
the "journal" (`internal/journal`). The consequential calls around it are the "trail"
(`internal/trail`, `libs/safetyscissors/trail`, `types/trail`, `trailrpc`, the console's
"Activity Trail"), stored under a directory named `activity`. A third package,
`internal/eventstream` with `types/events`, maps the first onto a subscriber envelope.
The [one-vocabulary rule](../glossary/one-vocabulary-rule.md) says two words for one
concept cost every reader a lookup.

[ADR 0050](0050-the-harness-is-a-module-named-safetyscissors.md) moved the trail's
event and readers into the module and left the journal in magus. A Go program that
imports the module can read what was called and cannot read what ran.

The numbers below were read on 2026-11-16 from the magus tree at `ad7804f24` and the SDK
branch at `1820f872c`, and from the stores on Eli's machine. The import table was read
again at `4584cc73a`, the commit the fold starts from.

### The two lines share almost nothing

- `journal.Event` has 23 JSON keys. `trail.Event`, with its inlined origin, has 30.
- They share 3: `ts`, `kind` and `duration_ms`. That is 3 of 50 distinct keys.
- The `kind` values do not overlap: 8 journal kinds, 10 trail kinds.

### One struct carries every kind

- `journal.Event` is one flat struct. Which of its 23 fields mean anything depends on
  `Kind`, and nothing says which.
- Of 7,031,252 journal lines in 5,153 files under 315 `runs` directories, 6,818,127
  (97%) are `output` lines, which use 7 of the 23 fields.
- 13 sites compare against `KindResult` before reading result fields.
- `KindWarn` has no producer in the tree and 0 lines on disk. `KindScope` has one
  producer, 127 lines, and 0 that carry a target, though the console reads one.

### The capture path borrows slog and uses none of it

- `journal.Emit` wraps the event in a `slog.Record` attribute. Six `EventFromRecord`
  calls in five files unwrap it again.
- About 80 of `capture.go`'s 256 lines are a fan-out handler, a discard handler, an
  attrs handler and `Enabled`, `WithAttrs` and `WithGroup` methods that no journal
  record uses.

### How far the names reach

| Import at `4584cc73a`       | Files | Replaced by                                        |
| --------------------------- | ----- | -------------------------------------------------- |
| `internal/journal`          | 66    | `libs/safetyscissors/journal`, `internal/recorder` |
| `internal/trail`            | 128   | `internal/recorder`, `internal/sessions`           |
| `libs/safetyscissors/trail` | 71    | `libs/safetyscissors/journal`                      |
| `types/trail`               | 14    | `types/feedback`                                   |
| `types/events`              | 8     | `libs/safetyscissors/journal/stream`               |
| `internal/eventstream`      | 1     | `libs/safetyscissors/journal/stream`               |
| `internal/handler/trailrpc` | 2     | `internal/handler/activityrpc`                     |

After the fold, 123 files import `libs/safetyscissors/journal`, 4 import
`journal/stream`, 166 import `internal/recorder` and 71 import `internal/sessions`.

`journal.Event` was named at 259 sites in 53 files, a `journal.Kind*` constant at 220 in
42, and `journal.Emit` was called at 24 in 15.

### Readers drop what they cannot read

- `cache.readEvents` and `trail.ReadRecent` skip a line that does not decode, without a
  word. 11 of the 7,031,252 journal lines do not decode.
- `journal.FileHandler` drops a failed write the same way.
- Both events carry a compatibility reader for `dur_ms`. 0 journal lines and 0 of
  167,137 activity lines in 276 files carry that key.

## Decision

There is one concept, the journal. The module's `journal` package holds the types and
readers for both kinds of line, `journal/stream` holds the subscriber view, and magus's
`internal/recorder` holds both writers. The full API, the rename table and each
decision's reasoning are in the fold's spec; the parts below are what a later reader
would be tempted to reverse.

| Part | What                                                                | State                                     |
| ---- | ------------------------------------------------------------------- | ----------------------------------------- |
| 1    | `journal.Record` and `journal.Call`, kept as two types              | done                                      |
| 2    | readers return `iter.Seq2[T, error]`                                | done                                      |
| 3    | the invocation id is a plain string                                 | done                                      |
| 4    | `recorder.Sink` replaces the slog capture bridge                    | done                                      |
| 5    | `journal/stream` replaces `internal/eventstream` and `types/events` | done                                      |
| 6    | `internal/sessions` sits above the journal                          | done                                      |
| 7    | nothing on disk changes; the `dur_ms` readers go                    | done                                      |
| 8    | the word trail leaves code, docs and UI                             | in progress (prose owed)                  |

### 1. Two line types, the first shaped like `slog.Record`

`Record` is one line of an invocation's journal. It is a concrete struct with `Time`,
`Invocation` and an unexported body. `Kind()` says what it carries, and one accessor
per kind returns `(T, bool)`: `Started`, `Finished`, `Exec`, `Output`, `Run`, `Scope`,
`Secret`. It implements `slog.LogValuer`, `MarshalJSON` and `UnmarshalJSON`. There are
no attrs, no message, no level and no handler.

`Call` is one line of the activity log, a struct with exported fields.

They stay two types because they share 3 of 50 keys.

A kind the reader does not know is kept: `Kind()` returns it, every accessor reports
false, and the raw line re-marshals byte for byte.

The module exports one value constructor, `NewRecord[B Body]`, because the writer is in
another module and cannot set an unexported field. It exports no writer.

### 2. One iterator rule

`Records`, `RecordsSince` and `Calls` yield `(value, nil)` or `(zero, err)`. A
`*codec.LineError` is one line that did not decode, and the read continues. Any other
error is the read failing, and it is the last pair. Each takes `ctx` first.

### 3. The invocation id is a plain string

`recorder.NewInvocationID` mints it and `journal.ValidInvocationID` guards every path
join. A reader refuses an id that fails the guard with an error wrapping
`fs.ErrNotExist`.

### 4. A sink is a function

`type Sink func(context.Context, journal.Record)`. `recorder.Emit` calls each sink on
the emitting goroutine. A sink is safe for concurrent use, does not block, sees one
goroutine's records in order and no order across goroutines, and says so once if it
stops recording.

### 5. The stream is a view

`journal/stream` holds `Event`, its bodies, `Filter`, `Writer` and `Follower`. Its wire
does not change and `Schema` stays 1.

### 6. Sessions sit above the journal

The session views in `internal/trail` (cost, session activity, prompt cache, the
invocation handler, touches) move into `internal/sessions`. `internal/recorder` imports
no `internal/sessions`, and the module's `journal` imports no `session`.

### 7. Nothing on disk changes

Paths, keys, key order and `omitempty` are what they are today. A round-trip test over
recorded lines holds it. A reader stops reading `dur_ms`.

### 8. The names

`internal/journal` becomes `internal/recorder`. `libs/safetyscissors/trail` becomes part
of `journal`. `trailrpc` becomes `activityrpc`, and the console's "Activity Trail"
becomes "Activity". `types/trail` becomes `types/feedback`, its `Trail` becomes
`feedback.Report`, and the Buzz namespace `magus\trail` becomes `magus\feedback`:
`types/journal` would collide with the module's `journal` in every file that imports
both. The `magus events` verb stays. A record is a line at rest and an
event is a record delivered to a subscriber, so `stream.Event` and the viewer proto's
`Event` keep the word.

### Principles checked

- [The Go proverbs](../design/principles/the-go-proverbs.md): a sink is a function, not
  an interface with one method and three it ignores; errors come back as values; a test
  fakes both with a literal.
- [No silent defaults](../design/judgment/no-silent-defaults.md): a line that does not
  decode is returned, a kind the reader does not know is kept, a missing journal is an
  error, and a sink that stops recording says so.
- [Everything is a file](../design/principles/everything-is-a-file.md): each reader
  takes the store's own directory, the encoding lives on `Record` and `Call`, and a
  test's fixture is a file.
- [Delete before you add](../design/principles/delete-before-you-add.md): the slog
  bridge, both `dur_ms` readers, `KindWarn`, two typed errors and four packages go.
- [A copy can be the right answer](../design/principles/a-copy-can-be-the-right-answer.md):
  `Record` and `Call` stay apart at 3 shared keys of 50. The two chunk decoders, in the
  cache and the follower, share semantics and bugs and merge into `RecordsSince`.
- [Break now, hold still later](../design/principles/break-now-hold-still-later.md) and
  [A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):
  every rename is made without an alias and recorded in `changes/unreleased`.
- [Abstract at the right layer](../design/principles/abstract-at-the-right-layer.md):
  the stream is a view over the journal, and the recorder is the one place that writes.
- [One source, every door](../design/principles/one-source-every-door.md): the cache,
  the viewer, the follower and an SDK caller read through the same four functions.
- [Clear is better than clever](../design/principles/clear-is-better-than-clever.md):
  the id is a string with a predicate, as `job.ValidID` and `session.ValidFileID` are.
- [Enforcement, not pinky promises](../design/principles/enforcement-not-pinky-promises.md):
  the import lists are asserted in the module's `doc_test.go`, the on-disk bytes by a
  round-trip test, and redaction of every free-text field by a test that reflects over
  the body types.
- [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
  the module takes no logger, installs none and wraps none. A host logs a `Record` with
  its own.
- [What we do not build is the product](../design/principles/what-we-do-not-build.md):
  ADR 0050 part 7 refuses a caller writing the store. `NewRecord` builds a value and
  writes nothing, so the refusal holds.
- [Measured, not asserted](../design/principles/measured-not-asserted.md): each
  deletion above carries its count.

No principle bends.

## Alternatives

### A. One flat struct for every kind

- The option: keep `journal.Event` as it is, 23 exported fields, and move it.
- Why it lost: the struct cannot say which fields a kind has. A reader of an `output`
  line sees a `Status`, a `CacheKey` and a `Command`, all empty, and learns by
  convention that they always are.
- Evidence: 97% of lines (6,818,127 of 7,031,252) use 7 of the 23 fields. 13 sites
  check `KindResult` before trusting the rest. A `scope` reader in the console reads a
  `Target` no producer has ever set, on 0 of 127 lines.
- What would reopen it: a measured cost in the accessor copy on the output path that
  the `BenchmarkEmitResult` pair shows and a flat struct would not pay.

### B. A struct with a sealed body interface

- The option: `Record{Time, Invocation, Body}` where `Body` is an interface with an
  unexported method, and a reader type-switches on it.
- Why it lost: Go does not check a type switch for exhaustiveness, so the sealed set
  buys nothing the compiler enforces. `Body` can be nil, which is a state every reader
  has to handle. And boxing an `Output` into an interface on every line allocates on
  the path that carries 97% of the volume.
- Evidence: the output count above. The allocation is reasoned, not benchmarked.
- What would reopen it: a linter in the gate that checks a switch over a sealed
  interface, or a benchmark that shows the boxing is free.

### C. A concrete record with a kind and accessors, as `log/slog` does

- The option: the one chosen. `slog.Record` is a concrete struct that a handler reads
  through methods, and `slog.Value` answers `Kind()` and then one accessor per kind.
- Why it won: an accessor returns a typed body or false, with no nil and no assertion.
  The struct keeps today's flat line as its unexported field, so the encoder that runs
  today still runs and the bytes cannot drift. A new kind is one accessor, and an old
  reader meets it as a kind it does not know.
- Evidence: the round-trip test over recorded lines, one per kind.
- What would reopen it: the kind set growing past what one accessor each reads well.
  Seven is fine. Twenty is not, and a generic `As[T]` would then replace them.

### `type InvocationID string`

- The option: a defined string type for the id.
- Why it lost: a defined string cannot hold "validated". `InvocationID(req.Name)`
  compiles, and an untyped constant converts on its own, so the guard runs at the path
  join whatever the type says. `io/fs` takes a plain `name string` and calls
  `fs.ValidPath` for the same threat.
- Evidence: the id crosses six boundaries that are strings on the far side
  (`secret.ContextWithInvocationID`, `types.AppendInvocationAncestor`,
  `types.WrapperInvocation`, the Connect requests, the `inv` key, the CLI argument). The
  module's three other id checks (`job.ValidID`, `session.ValidFileID`,
  `trail.ValidRef`) are predicates over a string, and `session.Record.Invocation` is a
  `string` holding the same value.
- What would reopen it: an id with parts a caller needs, such as a host or a time. That
  is a struct with a parser, not a defined string.

### An undecodable line as a kind

- The option: `KindMalformed` and a `Malformed()` accessor, with `iter.Seq[Record]`.
- Why it lost: `Call` has no place to put one, so the two readers would follow two
  rules. And `iter.Seq[Record]` has no place for a read that failed: an unreadable file
  and a cancelled context would end the sequence and look like a short journal.
- Evidence: 11 undecodable lines in 7,031,252, all of which `cache.readEvents` drops
  today.
- What would reopen it: nothing measured.

## Consequences

- A Go program that imports the module reads what ran and what was called, from one
  package.
- 66 files that import `internal/journal`, 128 that import `internal/trail` and 71 that
  import the module's `trail` change. No alias softens it.
- `internal/journal`, `internal/trail`, `internal/eventstream`,
  `libs/safetyscissors/trail` and `types/events` are gone.
- A reader that was content to lose a bad line now has to say so: it skips a
  `*codec.LineError` on purpose.
- A store written before `duration_ms` reads with zero durations. None was found on the
  one machine measured.
- A reader takes the directory that holds the files. `runs` and `activity` are spelled
  in magus only.
- The stream's wire still calls an invocation a run and a run a target result. The Go
  types in `stream` follow the wire.
- ADR 0050's State rows for `trail` change to `journal` in the same commit.

## Open questions

- The Buzz spelling `magus\session().trail()`. ADR 0050 keeps Buzz names as they are,
  and the ruling says the word leaves. Eli decides which holds here.
- The `runs/` directory. By the glossary it holds invocations. Renaming it orphans
  retained journals and blinds an older binary following it. It waits for the next
  change to the journal's layout.
- The stream's wire names. `run.started` names an invocation and `target.result` names
  a run. Renaming them is `Schema` 2 and breaks every subscriber. Not measured: how
  many subscribers exist outside the reference clients.
- `session.ReadInvocation` returns records and `journal.ReadInvocation` returns an
  invocation. Two readers in one module share a name and differ in what they return.
- A reader that lists the journals in a directory. `cache.ListRunLogs` and
  `stream.Follower` each list the directory themselves. No caller outside magus has
  asked.
- The blob records of an agent command and an agent spawn stay in magus until the guard
  moves into the module at step 8 of ADR 0050. Until then a module caller reads a
  `Call` and cannot decode its payload with a module type.
- The journal line carries no version. `Started.Version` is on the first line of each
  journal and is what a reader would branch on. Whether a line version is wanted before
  1.0 is not decided.
- Durations on stores from other machines were not measured.

## Amendments

### 2026-11-18: magus code stops knowing where the activity log lives

The fold's integration ran the repository's conformance ceiling, and where it and the
spec disagreed the ceiling won, ruled by Eli on 11-18.

- `recorder.CallsDir` is unexported. Six files outside `internal/recorder` joined the
  path themselves, which the store-path-export check refuses. `internal/recorder` gains
  two reader verbs instead: `Calls(ctx, base, limit)`, over `journal.Calls`, and
  `ReadPayload(base, payload)`. The module's readers still take the store's own
  directory (part D11 of the spec); only magus stops spelling `activity/`.
- Exports only their own package used are unexported, as the export-scope check
  requires: `AgentCommandAPIVersion`, `AgentSpawnAPIVersion` and `NameAgentSpawn` in
  `internal/recorder`, and `NewFactHandler`, `ActivityCall`, `ActivitySpawn` and
  `ReadActivity` in `internal/sessions`. A caller outside reads a session's activity
  through `sessions.ActivityHere`.
- The command shapes fold into `internal/recorder/feedback.go`, the prompt-cache clock
  into `internal/sessions/prompt.go`, and the feedback marks are
  `internal/recorder/marks.go`, as the file-split check asks of a file the fold created.
