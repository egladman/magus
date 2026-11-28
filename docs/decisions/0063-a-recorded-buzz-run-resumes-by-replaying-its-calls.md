---
title: "ADR 0063: a recorded buzz run resumes by replaying its calls"
order: 63
description: A `magus buzz --record` run appends every call from Buzz into a Go native to a JSON Lines log beside its output ref. After a crash the same command, run again, is answered from that log until it ends and then goes live. A VM snapshot on disk is refused, and so is resume for targets, judges, another machine or changed source.
tags: [adr, decision, buzz, gopherbuzz, durability, replay, journal]
status: proposed
date: 2026-11-27
---

# ADR 0063: a recorded buzz run resumes by replaying its calls

Evidence marks: **read** is source read in this checkout, **ran** is a run made for
this record, **prototype** is the scratch branch `worktree-agent-a5d65d03affdb6c63`
at `aa976aad7` (job `dx-replay-proto`, check `outcd195fb28c1a`), which is not merged
and not proposed for merge.

## Context

A multi-step Buzz script with effects, such as `hack/dev/merge-job-branches.buzz`
(1,498 lines), `rebase-branches-onto-base.buzz` (743) or `land-commits.buzz` (444),
dies with the process. Each of the three prints its steps under `--dry-run`; none
keeps a state file or resume logic (**read**, job `dx-stores`). A second run starts
from the top and meets whatever the first run left.

Three routes were weighed: replay from a recorded call log, a snapshot of the VM,
and the record alone. What the tree says about each:

**Every outside input reaches Buzz through one kind of value.** A Go native is a
`directObj` holding a `Callable`, and the interpreter calls `Fn` at four sites, now all
routed through `VM.interceptCall`: `libs/gopherbuzz/vm/vm.go:1038` (`OpCall`), `:1243`
(`OpInvoke`, the path `fs\readFile(p)` takes), `:2347` (`VM.Call`) and `:2420`
(`VM.Invoke`) (**read**). The clock and the random source are natives too: `os.time`
(`std/os.go:24`) and `std.random` (`std/core.go:32`, unseeded `math/rand/v2`). A map
iterates in insertion order from parallel `Keys` and `Vals` slices, never in Go map
order (`vm/value.go`). No native in `libs/gopherbuzz/std`, `std/` or
`internal/interp/bindings` runs a Buzz callback from another goroutine (**read**, job
`dx-replay-proto`). The exception is an FFI C callback (`vm/ffi.go:152`), which can
arrive at any time.

**The JIT never holds a frame at a native call.** `jitRun` enters native code only when
`len(vm.frames) == 1` (`vm/jit_shared.go:131-133`), and the eligibility filter
`depths` (`:322-488`) accepts 25 arithmetic, local and jump opcodes and returns
ineligible for every other one, `OpCall` and `OpInvoke` included (**read**). So a
native call always runs under the interpreter with `f.ip` already past the call.

**Replay from a full record is deterministic with no lint and no language rule.** The
prototype wrapped every reachable `directObj` and recorded name, argument digest and
result. A run crashed on its eighth call, inside a Buzz function. A fresh session ran
the same source against the record: seven calls were answered from it with zero real
invocations of `random`, `time`, `writeFile` and `readFile`, the open call ran again,
two more ran live, and the final value equalled an uninterrupted run. A changed call was
refused by name: "recorded proto.count[b58d15e89a95], live proto.writeFile[fd12068182c3]"
(**prototype**).

**Three things break naive replay, each shown by a failing variant.**

- A call that runs a Buzz callback cannot be answered from its recorded result.
  Answering `each(xs, fn)` from the record skipped the callback's writes to the Buzz
  heap and returned `45|0|351` where the real run gives `45|6|351` (**prototype**). The
  outer call has to run again, with the calls nested inside it answered from the record.
  Six generated callback adapters exist (`internal/interp/bindings/gen/callbacks.go`).
- A result over live Go state cannot be recorded. `File.open` returns a map of
  directs closed over an `*os.File` (`libs/gopherbuzz/std/io.go:71`). Re-running only
  that call truncated the file while the recorded `write` never reached the new
  handle: the script returned "hello world" and the file on disk was empty
  (**prototype**). The handle tables are worse, because they record cleanly:
  `screen\new`, `notify\new`, `progress` and `termkit` return an `int` index into a
  process-global table (`std/termkit/convert/convert.go:10-37`), and after a restart the
  same integer names nothing or another object.
- The call a crash interrupts has a beginning and no end. Its effect may have
  happened. Running it again is at-least-once.

**A snapshot has no such contract and a much larger build.** At a call in the
prototype's ten-call test script the VM reaches 73 heap objects, 41 of kinds no encoder
covers (30 `directObj`, 5 maps, 2 object definitions, a closure, an `Env`, a frame)
(**prototype**). `vm/marshal.go` encodes chunk constants only: null, bool, int, float,
str, enum definition, object declaration, pattern and type (`:252-307`). There are 17
heap kinds (`vm/value.go:42-63`). A heap reference is an index into a process-global
table with a free list (`vm/value_nanbox.go:162-226`), so no index survives a restart.
Captured variables are shared `cellObj`s and `Env`s are shared by pointer, so an encoder
needs an identity table. A frame's `ip` means something only against the same compiled
chunk, and nothing records the `CompileOptions` a chunk was built with
(`compiler.go:30-85`). `BytecodeVersion` is 22 and each bump exists because an older
reader would mis-execute (`vm/marshal.go:10-108`). Inside a callback the suspended outer
VM is not reachable at all, because a callback VM has no parent link (**prototype**). A
snapshot would still need the call record for the interrupted call.

**A script run already has an address and no chunk on disk.** `magus buzz --record`
stores the run under `interp.ScriptRunKey`, a hash of the script name, its source, the
lease and the arguments (`internal/interp/script_source.go:20-23`), so "a rerun adds an
attempt under the same ref and a changed probe gets a new one". The key covers the entry
file only; an imported file's text is not in it (**read**). A script session never
attaches a bytecode store (`internal/interp/script_run.go`), so a script compiles on
every run and there is no stored chunk to pin to.

**Cost.** The prototype's unoptimized recorder (SHA-256 and JSON per call) added 0.6 to
12.6 microseconds per call over five runs of 10,000 calls on a loaded machine, and wrote
161.7 bytes per call (**ran**, 2026-11-27). The built recorder is not measured. The VM's
builtin methods (`list.append`, `str.len`) are built by `newDirect` without a heap value
and never reach a module map; in the prototype's script with 20 direct calls, 13 were
module natives and 7 were builtins, and only the natives belong in a record.

**The failure is a machine going down.** Eli, 2026-11-27: the development machine has
run out of memory and rebooted often enough that work running at the time should come
back by itself. The refusals page already records one such event, a kernel panic reading
"100% of segments limit (BAD)" with Jetsam killing thousands of daemons
(`docs/design/refusals.md`, the pressure-brake row, 2026-10-31). Nobody has counted how
many of those events killed a recorded Buzz script, as against a target or a model
worker. Nothing in the repository records one: no refusals row names resume, replay or
snapshot, no ADR rules on them, and a search of `changes/` and `docs/` through `magus
query` found no such report (job `dx-stores`).

A reboot is a harsher crash than a killed process in two ways. A line written and not
yet flushed can be lost with the machine. And memory pressure is most likely to kill a
run inside a heavy call, such as `proc\exec`, so the interrupted call is the common case
and not the corner.

**Kubernetes has been considered and not designed.** Eli, 2026-11-27: running several
pods for availability is a past thought with manifests written and no decision to
deploy. It bears on this record in one way: of the routes weighed, only a record can
move between machines at all.

## Decision

Replay, built in two stages on one log. The log is useful alone and was built first.
Replay is the second stage and nothing in the first depends on it. Both are built on
this branch (Eli, 2026-11-27). The package is `internal/interp/replay`: `replay.Log` is
the file, `replay.Recorder` writes it, `replay.Player` reads it back, and
`vm.CallInterceptor` is the seam between them and the interpreter. Their types live in
the replay package, not in `types/`, because no script reads them.

magus never starts a run. Resume happens when the same command runs again, so what
re-issues the command after a reboot is whatever the person already runs: themselves, an
orchestrator session, a launch agent they own, or a pod's restart policy.

| behavior | enforcement | State |
| --- | --- | --- |
| The interpreter's four `Fn` call sites go through one seam, `vm.CallInterceptor`. A `directObj` made by `DirectValue` or `DirectClosure` reaches it; one made by `newDirect` for a builtin method does not. With no interceptor installed the seam is one nil check and one flag test. | `libs/gopherbuzz/vm/interceptor.go`; `TestCallInterceptorSkipsBuiltins`, `TestCallInterceptorSeesEveryNative`, `TestSessionWithoutObserverIsUnchanged` in `libs/gopherbuzz/session_test.go` | done |
| A `magus buzz --record` run appends each call to `calls.jsonl` under `<cache>/replay/<ref>/`, beside its output ref: one line when the call begins, one when it ends, keyed by sequence number, so the last line per key wins and a call with no end line is what a crash left. | `Log.begin`, `Log.end` and `Read` in `internal/interp/replay/log.go`; `TestStoreFoldsBeginAndEnd`, `TestStoreLeavesACrashedCallOpen`; `TestRecordedRunWritesCalls` in `cmd/magus/buzz_test.go` | done |
| The log's first line names its format (`magus.replay/v1alpha1`), the script and its arguments. A reopened log whose header differs is refused. | `TestStoreFoldsBeginAndEnd`, `TestAReopenedRecordMustBeItsOwn` | done |
| The log holds the sha256 of every file the run imported, as a line of its own (`{"imports":{...}}`) written once the entry chunk compiles and before it runs. The session hashes each file its imports read, in child and alias sessions too (`buzz.WithImportDigests`). A rerun whose digests differ is refused `import-changed`, naming the file and the `--restart` remedy, before the entry's top level runs. A record with no imports line is refused the same way, never read as no imports: a run that ended before its entry compiled writes `imports_unread` with the reason, and a crash before then writes nothing. Not covered: a file import runs that module's top level while the entry compiles, so a call made there comes before the imports line, and a changed import that changes such a call is refused as `diverged` at that call. | `TestImportDigestsArriveOnceBetweenCompileAndRun`, `TestAnErrorFromTheDigestsLeavesTheEntryUnrun` in `libs/gopherbuzz/session_test.go`; `TestAChangedImportIsRefusedNamingTheFile`, `TestARecordWithoutImportsIsRefused`, `TestARunThatEndsWithoutImportsSaysSo`; `TestAChangedImportRefusesTheRerunNamingTheFile`, `TestAnUnchangedImportResumes` in `BuzzRecordSuite` | done |
| An entry holds the call's name, its parent call, a digest of its arguments, and its result as JSON that keeps int, float, map and object apart. A result that holds a function, a foreign pointer or a native payload is recorded as `unencodable` with the reason and where. | `internal/interp/replay/codec.go`; `TestCodecRoundTrips`, `TestStdJSONDoesNotKeepAFloatApart` | done |
| Every native module is classed `answer` or `live` in a table in `internal/interp/replay/class.go`, and a test walks the generated registry and gopherbuzz's std and fails on a module with no class. `live` is a module whose results name state in this process: a handle table, an open file, a socket, a listener. | `TestEveryModuleHasAReplayClass`, `TestAModuleHoldingProcessStateIsLive`, `TestAModuleOfDataIsAnAnswer` in `internal/interp/replay/class_test.go` | done |
| A native may be run again after a run died inside it only if it is on the reads-only list `repeatable` in `class.go`. An unlisted native is not repeatable, and the resume refuses at it. | `TestOnlyReadsAreRepeatable` | done |
| A begin line is synced to disk before its call runs. An end line is not synced; the next begin or `Close` syncs it, and a reboot before then leaves the call open. | `Log.begin` and `Log.append` in `internal/interp/replay/log.go` call `Sync`; no test observes the sync | done |
| One process writes a log at a time. A second `--record` run of the same ref while the first is alive is refused, naming the holder's pid. | `TestStoreRefusesASecondWriter` | done |
| `magus buzz --record <script>`, run again with the same arguments, finds the open log under its ref and replays it. No flag asks for this. A call with a recorded end and no nested calls is answered from the log. A call with nested calls runs again and its nested calls are answered. Past the log's end the run is live and keeps appending. | `replay.Player`; `TestAnEndedCallIsAnsweredWithoutRunning`, `TestACallWithNestedCallsRunsAgainAndItsNestedCallsAreAnswered`, `TestPastTheRecordTheRunIsLiveAndAppends`, `TestARerunResumesAnInterruptedRecordedRun` | done |
| A run that reaches its end on its own writes a footer line. The next run of the same command finds a finished log and starts a new attempt. A run its context cut off writes none. | `Log.Finish`, `Finished`; `TestAFinishedRecordIsMarkedAndRefusesAppends` | done |
| Resume refuses, naming the call and the remedy, when: the next live call differs from the next recorded one in name, parent or arguments; a recorded call is `unencodable`, or belongs to a `live` module or to a module with no class; the interrupted call is not repeatable; or the run ends having made fewer calls than the log holds. No log is not a refusal: the run is recorded fresh. | `refusalError` in `player.go`; `TestADifferentCallIsRefusedNamingBoth`, `TestAnUnencodableResultIsRefused`, `TestACallToALiveModuleIsRefused`, `TestAnInterruptedCallThatIsNotRepeatableIsRefused`, `TestARunThatDoesLessIsRefused`; the same four at the command in `BuzzRecordSuite` | done |
| `--restart` discards the open log and runs from the top. It needs `--record` and is refused without it. Every refusal ends with the same remedy line. | `TestRestartDiscardsTheRecord`, `TestRestartNeedsRecord`, `TestARestartWithoutRecordIsRefused`; `remedy` in `player.go` | done |
| A `--test` run keeps no log. An unrecorded run opens none and pays nothing. | `TestATestRunKeepsNoCalls`, `TestAnUnrecordedRunOpensNoLog` in `internal/interp/script_run_test.go` | done |
| A failed recorded run's `ref` line says which call it died in. | `TestAFailedRunNamesTheCallItDiedIn` | done |
| `magus query output <ref>` shows the calls of the run. | none built | planned |
| A script reads a run's log as typed Buzz values. | none built; see Open questions | planned |
| A session's start lists each recorded run whose log is open, with the command that resumes it. | the rehydrate hook (`docs/guides/integrations/agents/magus-rehydrate.buzz`), not built | planned |
| The flush per call and the seam's nil check are benchmarked before the seam is called cheap. | none built | planned |
| magus starts no run by itself: no daemon, no launch agent, no boot item. | review; the refusals row below | planned |

What magus refuses here:

| what | why |
| --- | --- |
| a VM snapshot on disk | an encoder for 13 more heap kinds and an identity table, re-proved on every bytecode and layout change, for a state file no one can read; it still needs the log for the interrupted call |
| resume of a target, an op, a guard rule or a hook | an op already replays from the output store by its key, and a judge runs in milliseconds and is pure by ADR 0039 |
| resume after the script or an import changed | the run ref pins the entry source and arguments; import digests are planned to pin the rest; a changed run starts from the top. No `GetVersion` |
| resume on another machine or pod | not built. A log replays answers, not the world: a file the first machine wrote is not on the second, so only a script whose effects all land outside the machine could move. It also needs a shared store and a claim two machines honor. Nothing here forecloses it: the log is addressed by ref, an entry holds a digest of its arguments and no path, and one writer holds a log at a time |
| several pods running one script at once | a coordinator that hands out work is the server and queue refused below |
| a magus-owned launch agent or daemon that restarts runs at boot | magus computes and refuses; whoever runs the command owns the clock and the restart |
| a determinism lint or a rule in the Buzz language | the seam sees every outside input, so there is nothing for a lint to catch; gopherbuzz stays upstream's language |
| restoring a handle by token | a `live` module refuses instead; a handle protocol is a second lifetime model in every such module |
| recording a run that was not asked to record | `--record` stays the switch; a guard rule or a target body pays nothing |
| a retry policy, a timer, a queue or a server | the files are the history and the operator owns the clock |
| replaying a run that made an FFI call | a C callback can arrive at any time, so call order is not reproducible |

### Principles checked

- [Everything is a file](../design/principles/everything-is-a-file.md): the durable
  state is one JSON Lines file per run that `cat`, `grep` and `tail` read. A VM image
  would be the first binary state file magus writes for a run.
- [No silent defaults](../design/judgment/no-silent-defaults.md): a native with no
  replay class is refused by name, an unlisted native is treated as not repeatable and
  the refusal says so, and every refusal names its call.
- [A cache is an accelerator](../design/principles/a-cache-is-an-accelerator.md): this
  is the principle the decision strains. A log answers a call whose effect is not
  reproducible, so deleting it changes what a resumed run does. It holds in the weaker
  form: with the log deleted the run starts from the top, loudly, and is never wrong.
- [Automation you can interrogate](../design/judgment/automation-you-can-interrogate.md):
  the log shows a script run's inputs call by call, and it ships before the behavior
  that reads it.
- [Capability is not a reason](../design/principles/capability-is-not-a-reason.md):
  the failure is a machine that runs out of memory and reboots, reported by Eli and not
  yet counted. The log removes a failure that is on hand (a dead script says nothing
  about where it stopped) and supplies the count. Kubernetes is a capability with no
  failure behind it, and stays out.
- Magus computes and refuses: resume rides on the command being run again, so magus
  starts nothing.
- [The checker catches it](../design/principles/the-checker-catches-it.md): a test, not
  a resume, finds the native module that has no replay class.
- [What we do not build is the product](../design/principles/what-we-do-not-build.md):
  eleven refusals against the rows above.
- [Determinism at chosen points](../design/judgment/determinism-at-chosen-points.md):
  the seam is the chosen point; between two calls Buzz is already deterministic.
- gopherbuzz follows upstream: the seam is embedding API that no Buzz program can
  observe, and the language gains no syntax or built-in.

## Alternatives

**A VM snapshot at each call.** It needs no determinism and re-runs nothing. It lost on
cost and on what it cannot hold. Evidence: the census above (41 of 73 objects have no
encoder), process-local heap indexes, shared cells and `Env`s, an `ip` bound to an
unrecorded compile, and a callback VM that cannot reach its caller. Live state is lost
under both routes, so a snapshot buys exactness only for the pure part, which replay
recomputes in CPU time. Reopen if a run must resume whose replay takes longer than a
person will wait, measured on a real script.

**The log alone, with no replay.** This is stage one, and it was the first
recommendation, made when no failure was named: replay puts a declaration on every
module, a wrong one fails silently, and a crash inside a non-repeatable call ends in a
refusal anyway. It lost when Eli named the reboots. Reopen, by stopping after stage one,
if the first month of logs shows the killed runs were not Buzz scripts.

**A flag that asks for resume.** `--resume` keeps the person in the decision, and a pod
or a launch agent that restarts a command cannot pass a flag it does not know it needs.
Resume on rerun, with `--restart` as the way out, serves both, and is what is built.
Reopen if a rerun resumes a run someone meant to start over, more than once.

**magus restarts runs at boot.** The only fully hands-off form. It lost to the identity
rulings: no daemon, the operator owns the clock. Reopen only with those.

**Design for Kubernetes now.** Replay is the route that could cross machines, which is a
reason to choose it, not a reason to build the crossing. The use is not decided, and the
hard parts (effects on a local disk, a claim two machines honor, a shared store) each
need a failure to be judged against. Reopen when a deployment is chosen.

**An effect class on every method and a lint for judges** (the prior study's decisions
14 and 15, which class every "host" method). For replay it is unnecessary: the prototype
replayed a run that read the clock and a random number with no classification at all,
because the seam records them. What replay needs is narrower: which modules return live
state, and which calls are safe to run twice. The lint for ADR 0039 is a separate
question that this decision neither builds nor blocks. Reopen for ADR 0039 on its own
evidence.

**Pinning to a chunk digest** (the prior study's decision 13). A script has no stored
chunk: it compiles on every run, and a `.bo` carries no content digest, only a magic and
a version (`vm/marshal.go:127-130`). The run ref already pins the entry source and
arguments. What it misses is imports, which the header's planned digests would cover.
Reopen if scripts start loading from the bytecode store.

**Running the interrupted call again without asking.** The prototype does this and the
result is correct for a counter. For `proc\exec` of a merge it repeats an effect.
Refusing with `outcome-unknown` unless the native is on the reads-only list keeps the
person in the decision. Reopen with a count of how often resume refuses on a call a
person then reruns by hand.

**Wrapping module values instead of a seam in the interpreter.** The prototype's route,
forced by its write paths. It misses a native that another native returns at run time (a
`File`'s methods) unless results are walked too, and it cannot tell a builtin from a
native except by name. A seam at the four call sites sees every call once.

**The first build: `internal/hostcall`, with its types in `types/`.** The package was
first built under that name, with the entry and refusal types in `types/` so a typed
Buzz read could be generated from them. It was moved to `internal/interp/replay` and
renamed before merge, for two reasons. The graph showed one importer, `cmd/magus`, and no
script reader, so the types had no business in `types/`. And Eli ruled the qualifier
"host" out of the names: it said where a call came from and nothing about what the
package does. Reopen the move of the types if a typed read of the log is built.

## Consequences

A script run under `--record` writes about 160 bytes per call, by the prototype's
measure: a run that makes ten thousand calls writes 1.6 MB. The built log is not
measured.

A failed recorded run says which call it died in on its `ref` line, and what ran before
it is in `calls.jsonl`, whether or not anyone resumes it.

A script that opens a `File`, a socket, a screen or a progress bar cannot be resumed
past that call. A script that wants resume keeps to modules classed `answer`. `io`,
`iocore`, `ffi`, `buffer`, `pipe`, `screen`, `notify`, `progress`, `termkit`, `layout`,
`pick` and `recorder` are `live`, and so are `fs.watch`, `http.server`, `image.open`,
`term.spin` and the `magus` service and lock calls (`class.go`).

A new native module's author must add it to a table in `class.go` before the test
passes. The class is no field on the module, so the registry carries no new declaration.

Replay makes the cost of a resume CPU time on the pure Buzz between calls. It spends no
tokens and runs no effect twice, except a call on the reads-only list.

The interpreter's hot path gains a nil check and a flag test per native call. The
baseline is 68 to 1,474 nanoseconds per native call in the prototype's runs. The seam
has no benchmark yet.

A reboot between a begin line and its call's effect leaves the call open and, for a
native that is not on the reads-only list, refuses the resume. The person reruns with
`--restart`.

## Open questions

- How many of the out-of-memory reboots killed a recorded Buzz script. If most killed
  targets or model workers, this decision brings little back, and the count from the
  first month of logs says so.
- Whether a deployment to Kubernetes is wanted. If it is, the shared store, the
  cross-machine claim and the rule for effects on local disk are one further ADR.
- Whether the reads-only list is right. It is a first cut in `class.go`, written by
  hand over the modules and not counted against the generated methods.
- What a flush per call costs. It is required now, because the failure is a reboot, and
  it is not measured. A script making ten thousand calls makes ten thousand flushes.
- Where a large result goes. The entry holds the result inline; no blob store keyed by
  digest was found (job `dx-stores`), and an `fs\readFile` of a large file puts the file
  in the log.
- Whether a script should read a run's log as typed values. It was dropped with the
  types' move out of `types/`, because nothing needs it yet. A reader that is not Buzz
  reads the JSON Lines file.
- How many natives take a callback. Six adapter shapes exist; the methods that use them
  were not counted.
- Whether a suspended `ctx.needs` fiber (`internal/interp/bindings/target.go:515`, the
  one production caller of `vm.Suspend`) ever runs inside a recorded script. It is a
  target-body path and this decision excludes targets; no test shows a script cannot
  reach it.
- ADR 0039 and ADR 0051 are both `proposed`. The log is a new store beside the journal,
  not a journal event kind; whether it becomes one waits on 0051.
