---
title: "ADR 0039: judging is a pure computation, and recording it is a separate step"
order: 39
description: A guard judgment reads state and writes nothing, so the same call against the same state gets the same verdict. Recording is a second step keyed on the call's id, and repeating it is a no-op, so a re-fired hook or a retry writes one trail row and spends fire-once advice once. An embedder that is not a coding assistant judges its own tools through one exported door, magus.JudgeTool, over tool rows it declares at Open. Rules read capabilities, one new capability marks an external, irreversible act, one exported enum carries the four decisions, ask needs an option saying the embedder can prompt, and the spawn rule becomes optional.
tags: [adr, decision, guard, sdk, agents, harness, idempotence]
status: proposed
date: 2026-11-09
supersedes: "the dry-run flag on a guard request (internal/guard Request.DryRun)"
---

# ADR 0039: judging is a pure computation, and recording it is a separate step

## Context

Eli asked on 2026-11-08 for the Go SDK's judging to serve an agent that is not a coding
assistant. He asked for two things at minimum: an interface through which an embedder
judges its own tools, and idempotent judging. He agreed to the shape recorded below. It
changes the root package's exported API, so it is recorded before any code. The
evidence below was read from this tree at 25b8902a7 on 2026-11-09. It also draws on the
read-only review of 2026-11-08 (`review-go-harness-sdk-vs-mastra`), which tried four
target harnesses: a support-ticket agent, a data-pipeline agent, an ops runbook agent and
a research agent.

### What the SDK offers today

- There are two entry points, `(*Magus).JudgeCommand(ctx, session, dir, command string)`
  and `(*Magus).JudgeWrite(ctx, session, dir, path string)`. Both return
  `(types.GuardJudgment, error)` (`judge.go:24`, `judge.go:30`). Each builds a
  `guard.Request` whose input is one string. A call that is neither a shell line nor a
  file write has no door. A support agent's `refund_order({id, amount})` has to be faked
  as a shell line, or it is refused.
- Judging writes. The doc comment says the judgment "is recorded on the workspace's trail
  as a hook's is" (`judge.go:17-18`), and
  [the SDK reference](../reference/go-sdk.md#judging-a-call-before-it-runs) adds that "a
  fire-once advice fires once".
- `guard.Request.DryRun` (`internal/guard/guard.go:650`) reaches the verdict with no
  writes. `guard.Judge` (`:753`) reads the flag at 19 sites, and session state is read
  from a temporary copy that `sessionCopy` writes and then discards (`:935-939`). A spawn
  or continuation under `DryRun` is denied outright: "a spawn or continuation cannot be
  checked, since judging one records it" (`:902-906`). The SDK sets `DryRun` only from
  `Run(WithDryRun())` (`judge.go:84`, `run.go:100`). The CLI sets it from `magus job
  fork` (`cmd/magus/job.go:432`).
- `Judge` writes at more than a dozen points before it returns. It records message
  topics, tool times, skill loads and agent usage. It joins a started child to its spawn,
  ends a stopped session's job, records the policy digest, releases a binding that ended
  and registers a checkout base. It spends fire-once advice through `hint.Gate.Once`
  (`internal/hint/marker.go:125`), and it appends the trail row through
  `appendHookActivity` and `appendHookSpawn`.
- The guide's harness calls `JudgeCommand`, then `Run`, and `Run` judges the same line
  again (`docs/guides/go-sdk.md:572`, `:589`, and "`Run` judges again either way"). Each
  allowed call writes two trail rows and spends its advice twice. A planner that previews
  a call changes the verdict the real call gets.
- The hook envelope models no call id (`hookEnvelope`, `internal/guard/guard.go:1835`).
  Nothing could key a repeat, even for a caller that wanted one.

### Tools, capabilities and decisions today

- The guard knows a tool only from the host's tool rows, `guard.Request.Tools`
  (`internal/guard/guard.go:690`). Those rows come from a directory that
  `magus agent harness install` writes from a harness spell's `mgs_listTools`
  (`internal/agent/tools.go:107`, `:160`). The SDK reference says "the harness is
  CLI-only". A tool that no row names is refused by `unjudged-tool` (`denyUnjudgedTool`,
  `internal/guard/tools.go`), and the refusal says to add an `mgs_listTools` row, which
  no embedder has.
- `types.ToolCapability` (`types/harness_contract.go:43`) has six values: runs, reads,
  writes, spawns, network and inert. All six describe effects on the workspace. Sending
  mail or issuing a refund counts as `network` or `inert`, and both pass. A
  `types.HarnessTool` row (`:150`) carries one capability.
- The guard judges a call by the shape of its input first and by capability second. A
  read is rewritten as `sed -n` or `cat` (`readLine`, `internal/guard/tools.go`), a
  host's search as the shell line it stands for (`nativeSearchLine`), and an MCP call to
  magus as a `magus` command line (`buildCall`). Rules end up matching strings the caller
  never typed.
- There are two decision vocabularies. `types.GuardJudgment.Decision` is a bare `string`,
  "one of agent.GuardDecisions" (`types/guard_judgment.go:10`), and that list (pass,
  advise, deny, ask) lives in an internal package (`internal/agent/guard.go:26`). The
  exported `types.GuardDecision`, which a workspace rule returns, has allow, advise and
  deny (`types/guard.go:64-73`), with no pass and no ask. The guide compares
  `j.Decision == "deny"`, and `judge.go:88` compares against `string(types.GuardDeny)`.
  Buzz policy in this repository spells `GuardDecision.allow` 179 times across 12 files.
- Only built-in rules produce `ask` (human-only and the push gate). When the caller cannot
  prompt, `RendersAsk` (`internal/guard/guard.go:695`) turns the ask into a deny
  (`:1409-1413`). The deny's reason says the hook "predates approval prompts" and should
  be re-merged from `magus describe harness` (`askUnrenderedDeny`,
  `internal/guard/push.go:335`). No SDK option sets `RendersAsk`. An SDK caller always
  gets that deny, and its text names a hook the caller does not have.
- `WithSpawnRule` is required: "Open and Inspect fail without one or on a nil one"
  (`register.go:99-104`). A program with no agents has to adopt model tiers before it can
  list projects.
- The principle page says "a guard verdict has three outcomes, allow, advise and deny"
  ([The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md),
  line 33). The code has four.
- The `types` subpackages are leaves under `types`. `magus explain dir:types/trail` lists
  six importers and no `imports` edge into `types`.

## Decision

This record leaves the following unchanged. The SDK has no agent loop, no model router
and no `Serve` or `Stream` ([refusals](../design/refusals.md), rows 61 and 62;
[the SDK reference](../reference/go-sdk.md#what-the-sdk-leaves-out)). magus computes and
refuses. It never runs the embedder's tool, never retries and never chooses what runs
next. The guard denies by default ([ADR 0013](0013-the-guard-denies.md)).

### 1. A judgment is a pure computation

`JudgeTool` and the internal `guard.Judge` read state and write nothing: no trail row,
marker, job store line or temporary copy. The same call against the same state gives the
same verdict.

- The clock is part of the call. The rules read `agent.ToolCall.At` in place of each
  `time.Now()` inside `Judge`. A zero `At` is an error that names the field.
- A judgment cut short by its deadline does not follow from state. It says so under
  `advisory-unfinished`, as today, and recording stores it as cut short.
- Some writes happen before `Judge` decides: a checkout base registered, a child joined
  to its spawn, a binding released. Those are now read through an in-memory overlay, as
  `withRegisteredBase` already does for a dry run. `sessionCopy` is deleted.
- A spawn or continuation can be judged without being recorded, so the `DryRun` refusal
  at `internal/guard/guard.go:902-906` is deleted.

### 2. Recording is a separate step, keyed on the call's id

`RecordJudgment` takes the call and its judgment and makes every write `Judge` makes
today. Every write carries the call's id. If the session has already recorded that id,
the call is a no-op. A re-fired hook or a retry with the same id cannot write a second
trail row, spend fire-once advice twice or register a spawn twice. The first record
stays.

- An empty id is an error. Nothing is recorded without a key unless the record says so.
  The open questions cover hosts that send no id.
- A judgment carries the id it was made for (`Judgment.Call`). Recording it against a
  call with another id is an error.
- The judgment lists the fire-once advice it showed (`Judgment.Spends`), and recording
  marks that advice spent. Recording does not run the rules again.
- The recorded ids live in one append-only `calls.jsonl` in the session's state
  directory, one line per id, following
  [Everything is a file](../design/principles/everything-is-a-file.md).

The hook path (`magus shell`) does both steps, in order. A planner previewing a call does
only the first. `Run` judges and records its own lines. `WithCallID(id)` makes `Run`
record under the call the embedder judged, so the guide's harness writes one row per
call. This replaces the dry-run flag: `guard.Request.DryRun` is deleted, and
`Run(WithDryRun())` and `magus job fork` judge without recording.

### 3. An embedder judges its own tools through one door

An embedder declares its tools once at `Open` with `WithToolRows` and judges each call
with `JudgeTool`. A row gives the tool's name and capabilities, and says where in the
call's input to find paths or a shell command. Each location is a JSON Pointer (RFC 6901)
to a string or an array of strings.

- The built-in shell rules and `magus\guard.command` judge a `runs` row's command. The
  path rules and `magus\guard.write` judge a `reads` or `writes` row's paths. The spawn
  rule judges a `spawns` row. After that, `magus\guard.tool` and any `WithToolRule` judge
  every declared row. The strictest decision wins: deny, then ask, then advise, then
  pass.
- A row may name more than one capability, and each is judged. A tool that reads a file
  and sends it by mail is `reads` and `external`.
- `unjudged-tool` still refuses an undeclared tool. Its reason names the tool and the
  place to declare it: `magus.WithToolRows` on the SDK path, `mgs_listTools` on the hook
  path.
- A malformed row fails `Open`, because misconfiguration is an error. A row is malformed
  when it has no name, repeats a name, has an unknown capability or holds a pointer that
  does not parse.
- `JudgeCommand` and `JudgeWrite` are deleted. A shell tool becomes a `runs` row with a
  `Command` pointer, and a file editor becomes a `writes` row with `Paths`.

### 4. Rules read capabilities; one new capability marks an external, irreversible act

`agent.CapabilityExternal` (`"external"`) marks a call that acts on the world outside the
workspace and cannot be taken back: sending mail, issuing a refund, paging a person or
deleting a customer record. A new built-in rule, `external-act`, answers `ask` for it.
Without `WithRendersAsk` that ask becomes a deny that says so (part 5). A workspace can
lower the rule by name in `magus\guard.builtins`, as it can any deny under ADR 0013.

`magus\guard.tool` and `WithToolRule` receive the capabilities and the paths and command
read from the input. They never receive a rewritten shell line.

Here the code disagrees with the decision, and this record says so instead of bending the
decision to fit. Host tools keep the shape-first path: reads become `sed`/`cat`, host
searches become shell lines, and magus MCP calls become `magus` lines
(`internal/guard/tools.go`, `buildCall`). This record does not extend that path to
declared rows, and it does not remove it for host tools either. Whether the removal goes
in the same change is an open question.

### 5. One decision enum with four outcomes, and an option to render ask

`agent.Decision` is the only decision type. Its values are pass, advise, deny and ask,
spelled on the wire as the glues parse them today. `Judgment.Decision` and a rule's
`Verdict.Decision` both have type `agent.Decision`, and a rule's zero value is pass. On
the rule side, `allow` becomes `pass`. `agent.GuardSchemaVersion` stays at 1, because no
wire value changes meaning, and under that rule the new optional fields `Call` and
`Spends` need no bump.

An ask never clears a deny. The strictest decision wins, so a workspace rule's ask cannot
override a built-in deny. An ask means consent to a costly or external act. That is the
only use the [record of refusals](../design/refusals.md) (row 21) leaves open since the
blanket ask was reverted.

`WithRendersAsk()` is the embedder's statement that it can put an ask in front of a
person. Without it, an ask becomes a deny that names the act. Its reason says "this
program declared no way to ask a person (magus.WithRendersAsk)". It does not reuse the
hook glue's re-merge text.

### 6. Rules are registered per workspace, not per process

Tool rows, a Go tool rule, the spawn rule and the ask option are all `Open` options, held
by the `*Magus` they configure. Buzz rules stay in the workspace's magusfile. This record
adds nothing to a package-level registry such as `project.DefaultSpellRegistry`. A
process that serves several workspaces gives each one its own rows and rules.

### 7. The spawn rule is optional

Neither `Open` nor `Inspect` requires `WithSpawnRule` any more. Without one, a new rule,
`spawn-rule-unset`, denies every spawn, and its reason names `magus.WithSpawnRule` and
`magus.TierCeiling`. The CLI keeps passing `TierCeiling`.

### Where the names live, and their signatures

The agent types move out of top-level `types` into a new package,
`github.com/egladman/magus/types/agent`. The package imports `types` (for `NextStep`,
`LeaseSource`, `GuardProfile` and `ModelTier`), and `types` never imports it. So every
type in `types` that names a moved type moves too: `AgentFacts` (its `Shadow` map),
`RuleDoc` (its `Verdicts`) and `HarnessTools` (its `Rows`). The package name
collides with `internal/agent`, so internal callers alias one of the two.

In `types/agent`:

```go
type Decision string // replaces types.GuardDecision and internal/agent's decision list
const (
    DecisionPass   Decision = "pass"
    DecisionAdvise Decision = "advise"
    DecisionDeny   Decision = "deny"
    DecisionAsk    Decision = "ask"
)
func (d Decision) Values() []string
func (d Decision) Valid() bool
func (d Decision) String() string
func (d Decision) MarshalText() ([]byte, error)
func (d *Decision) UnmarshalText(text []byte) error

type Capability string // replaces types.ToolCapability
const (
    CapabilityRuns     Capability = "runs"
    CapabilityReads    Capability = "reads"
    CapabilityWrites   Capability = "writes"
    CapabilitySpawns   Capability = "spawns"
    CapabilityNetwork  Capability = "network"
    CapabilityInert    Capability = "inert"
    CapabilityExternal Capability = "external" // new
)
func Capabilities() []Capability // replaces types.ToolCapabilities

type ToolRow struct { // replaces types.HarnessTool
    Name                  string       `json:"name"`
    Capabilities          []Capability `json:"capabilities"`
    Paths                 []string     `json:"paths,omitempty"`   // JSON Pointers into the input
    Command               string       `json:"command,omitempty"` // a JSON Pointer into the input
    SpawnDescriptionField string       `json:"spawn_description_field,omitempty"`
}

type HarnessTools struct { // moved from types; Rows is now []ToolRow
    Dir  string    `json:"dir"`
    Rows []ToolRow `json:"rows"`
}

type ToolCall struct {
    ID      string         `json:"id"`
    Tool    string         `json:"tool"`
    Input   map[string]any `json:"input"`
    Session string         `json:"session,omitempty"`
    Dir     string         `json:"dir"`
    At      time.Time      `json:"at"`
}

type Judgment struct { // replaces types.GuardJudgment; existing fields keep names and tags
    SchemaVersion int                `json:"schema_version"`
    Decision      Decision           `json:"decision"`
    Reason        string             `json:"reason,omitempty"`
    Context       string             `json:"context,omitempty"`
    Rule          string             `json:"rule,omitempty"`
    Lease         string             `json:"lease,omitempty"`
    LeaseFrom     types.LeaseSource  `json:"lease_from,omitempty"`
    Next          []types.NextStep   `json:"next,omitempty"`
    Call          string             `json:"call,omitempty"`   // new
    Spends        []string           `json:"spends,omitempty"` // new
}

type DeniedError struct{ Judgment Judgment } // replaces types.GuardDeniedError
func (e *DeniedError) Error() string

type Verdict struct { // replaces types.GuardVerdict: what a rule returns
    Decision Decision
    Reason   string
    Rule     string
}
func Stricter(a, b Verdict) Verdict       // replaces types.StricterGuardVerdict; ask ranks between deny and advise
func ValidateRuleName(name string) error  // replaces types.ValidateGuardRuleName; "tool" joins the reserved seams

type ToolRequest struct { // what magus\guard.tool and a ToolRule read
    Call         ToolCall
    Capabilities []Capability
    Paths        []string
    Command      string
    Lease        string
}

// Moved from types, fields unchanged: Facts (was types.AgentFacts; Shadow is
// map[types.GuardProfile]Decision) and RuleDoc (Verdicts is []Decision).

const GuardSchemaVersion = 1 // moved from internal/agent
```

In the root package `magus`:

```go
func (m *Magus) JudgeTool(ctx context.Context, call agent.ToolCall) (agent.Judgment, error)
func (m *Magus) RecordJudgment(ctx context.Context, call agent.ToolCall, j agent.Judgment) error
func WithToolRows(rows ...agent.ToolRow) Option
func WithRendersAsk() Option
type ToolRule func(ctx context.Context, req agent.ToolRequest) (agent.Verdict, error)
func WithToolRule(rule ToolRule) Option
func WithCallID(id string) RunOption
// signatures unchanged, no longer required:
type SpawnRule func(parent, child types.ModelTier) error
func WithSpawnRule(rule SpawnRule) Option
```

A `ToolRule` error means the rule judged nothing, and it is handled the way a failed
workspace rule phase is today: a call that changes state is denied, and a read passes
with a note. Each new root name gets a line in `hack/lint/sdk-exports.jsonl`, and the
lines for `Magus.JudgeCommand` and `Magus.JudgeWrite` are removed.

In Buzz, `magus\guard.tool(<fun>)` is a fifth guard seam beside command, write, spawn and
stop. It takes a `ToolRequest` and returns a `Verdict`. buzzgen generates the Buzz
mirrors ([ADR 0035](0035-buzz-bindings-come-from-one-config-driven-generator.md)), and
no binding is written by hand.

Internally, `guard.Judge(ctx, deps, req)` keeps its shape and becomes pure. A new
`guard.Record(ctx context.Context, deps Dependencies, req Request, j agent.Judgment) error`
makes the writes.

| part                                                            | where                                                     | State                    |
| --------------------------------------------------------------- | --------------------------------------------------------- | ------------------------ |
| a verdict with no writes, by flag                               | `guard.Request.DryRun`, `sessionCopy`                     | done (deleted by part 2) |
| `ask` turned into a deny without `RendersAsk`                   | `internal/guard/guard.go:1409`, `askUnrenderedDeny`       | done                     |
| an undeclared tool refused                                      | `unjudged-tool`, `denyUnjudgedTool`                       | done                     |
| host tool rows from an installed directory                      | `internal/agent/tools.go` `readToolRows`, `FindToolRows`  | done                     |
| a spawn rule required by `Open` and `Inspect`                   | `register.go:99`                                          | done (relaxed by part 7) |
| `JudgeCommand`, `JudgeWrite`                                    | `judge.go:24`, `:30`                                      | done (deleted by part 3) |
| a pure `guard.Judge`, the clock from `ToolCall.At`, an overlay  | `internal/guard/guard.go`                                 | proposed                 |
| `guard.Record` and `Magus.RecordJudgment`, keyed on the call id | `internal/guard`, `judge.go`, the session's `calls.jsonl` | proposed                 |
| the call id read from the hook envelope                         | `hookEnvelope`                                            | proposed                 |
| `Magus.JudgeTool`, `WithToolRows`, `WithToolRule`               | `judge.go`, `register.go`                                 | proposed                 |
| `magus\guard.tool`                                              | `std/magus.go`, `internal/guard`                          | proposed                 |
| the `types/agent` package and the moves into it                 | `types/agent`; the old names deleted from `types`         | proposed                 |
| `CapabilityExternal` and the `external-act` rule                | `types/agent`, `internal/guard`                           | proposed                 |
| `WithRendersAsk` and its own deny text                          | `register.go`, `internal/guard/push.go`                   | proposed                 |
| `spawn-rule-unset` when no `WithSpawnRule` is given             | `register.go`, `internal/guard`                           | proposed                 |
| `WithCallID` on `Run`                                           | `run.go`                                                  | proposed                 |
| an agent loop, a model router, `Serve` or `Stream` in the SDK   | none                                                      | not built                |
| a dry-run option on `JudgeTool`                                 | none                                                      | not built                |

### Principles checked

- [Verdicts are provenance-blind](../design/principles/verdicts-are-provenance-blind.md):
  a declared row is judged by what it does, not by who calls it. The hook path and the
  SDK path both reach one `guard.Judge`.
- [ADR 0013: the guard denies](0013-the-guard-denies.md): still holds. An undeclared tool
  is denied, `spawn-rule-unset` denies, and an ask the caller cannot render becomes a
  deny. `external-act` asks rather than denies, because the remedy is a person's consent
  and not a different command. Without `WithRendersAsk` it denies, so the default still
  holds.
- [A record of refusals](../design/refusals.md), row 21 (the reverted blanket ask): not
  reopened. An ask still never clears a deny, the strictest decision wins, and ask
  applies only to consent for an external or costly act.
- [Human in the loop](../design/principles/human-in-the-loop.md): `WithRendersAsk` gives
  an embedder with an approval UI a named point where a person decides. The trail
  records the ask and its answer like any other judgment.
- [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
  a judgment allows, advises, asks or denies, and none of the four rewrites the call. The
  page's "three outcomes" is already wrong. The implementing change corrects it to four
  in the same commit.
- [The model proposes, the harness executes](../design/principles/the-model-proposes-the-harness-executes.md),
  with [refusals](../design/refusals.md) rows 61 and 62: magus judges the embedder's tool
  call and never runs the tool. `RecordJudgment` writes only magus's own records.
- [A harness is not an agent thing](../design/principles/a-harness-is-not-an-agent-thing.md):
  the external capability and the tool door serve a support or ops program as well as a
  coding host.
- [Capability is not a reason](../design/principles/capability-is-not-a-reason.md): each
  addition removes a measured failure. `refund_order` is refused or faked as a shell
  line. An irreversible act passes as `network` or `inert`. A preview spends advice. The
  guide writes two rows per call. `ask` never reaches an SDK caller.
- [Delete before you add](../design/principles/delete-before-you-add.md): this deletes
  `DryRun` and its 19 branches, `sessionCopy`, `JudgeCommand`, `JudgeWrite`, the internal
  decision list and the `allow` spelling. The judging methods stay at two: `JudgeTool`
  and `RecordJudgment` replace `JudgeCommand` and `JudgeWrite`.
- [No silent defaults](../design/judgment/no-silent-defaults.md): a missing spawn rule, a
  missing ask option, an undeclared tool, a zero `At` and an empty call id each fail
  loudly and name the fix.
- [Everything is a file](../design/principles/everything-is-a-file.md): the recorded ids
  are one append-only JSON Lines file, where the last line per key wins.
- [One source, every door](../design/principles/one-source-every-door.md): the hook, the
  CLI, the SDK and `Run` all reach one `guard.Judge` and one `guard.Record`. The Buzz
  types are generated from the Go ones.
- [Break now, hold still later](../design/principles/break-now-hold-still-later.md):
  every break listed under Consequences is made without a shim, alias or dual reader.

## Alternatives

### Keep the dry-run flag and export it as a judging option

This would add `JudgeOption` and `WithDryRun` to `JudgeCommand`. It lost because one
function would keep two write disciplines, and `guard.Judge` reads the flag at 19 sites
today. A branch that forgets the flag writes during a preview, and nothing tests for a
write that should not happen. `sessionCopy` would still write temporary files, and a
spawn still could not be previewed. Reopen if splitting the steps costs the hook more
than its seven-second budget, measured on recorded sessions.

### Make `Judge` deduplicate on the call id itself

This keeps one step, with `Judge` skipping writes for an id it has already seen. It lost
because a planner's preview would become the call's record. The real call carries the
same id, so it would record nothing, and a preview must not count. These grounds never
change, so this stays closed.

### Have `Record` run the rules again

Here `RecordJudgment(ctx, callID)` judges a second time and writes what it finds. It lost
on cost and on accuracy. The hook would run every rule twice inside one budget, and since
state can change between the two runs, the record could disagree with the verdict the
caller acted on. `Judgment.Spends` carries the one fact recording needs from the rules.

### Restate an embedder's tool as a shell line or a write

This is the status quo: an embedder builds `refund_order 42 10.00` and calls
`JudgeCommand`. It lost because rules would match strings no caller typed, as the review
found with `affectedLine` and the guide's `strings.Cut`. A refund has no shell shape for a
rule to read. This stays closed.

### Fold external acts into `network`

It lost because `network` passes. A refund judged as `network` would run without a word
to a person. A rule has to tell reaching a host apart from committing an act.

### Turn ask on for every SDK caller

This assumes every embedder can prompt. It lost on the evidence of row 21: a caller that
cannot prompt renders an ask as nothing, and two of four host glues would have let every
denial through that way. The caller has to state that it can prompt.

### A process-wide rule registry

Rows and Go rules would register on a package-level registry, the way targets do today
through `project.DefaultSpellRegistry`. It lost because that registry panics on a
duplicate (`project/registry.go:43-44`), forces a hand-written unregister in the guide,
and cannot give two workspaces in one process different rules.

### Default the spawn rule to `TierCeiling`

It lost because it is a silent default: a program that never thought about tiers would
run under one. A deny that names the option is loud, and a program that never spawns
never sees it.

### Install tool rows for the SDK through `magus agent harness install`

The embedder would write a harness spell and install its rows directory. It lost because
that path is CLI-only, writes files to disk, and asks a support program to become a host
harness. Rows passed at `Open` carry the same record without the install.

## Consequences

The breaks are deliberate and recorded under
[A deliberate break never merges red](../design/principles/a-deliberate-break-never-merges-red.md):

- `Magus.JudgeCommand` and `Magus.JudgeWrite` are deleted, and callers move to
  `JudgeTool` with a declared row.
- These names move from `types` to `types/agent` under the names above, with no alias
  left behind: `GuardJudgment`, `GuardDeniedError`, `GuardDecision` (with `GuardAllow`,
  `GuardAdvise` and `GuardDeny`), `GuardVerdict`, `StricterGuardVerdict`,
  `ValidateGuardRuleName`, `ToolCapability` (with `ToolRuns` through `ToolInert`),
  `ToolCapabilities`, `HarnessTool`, `HarnessTools`, `AgentFacts` and `RuleDoc`.
- `HarnessTool.Capability` becomes `ToolRow.Capabilities`, which changes the
  `mgs_listTools` rows of every shipped harness spell. A name can now appear in more than
  one capability file of a tool rows directory. `ToolRowsVersion` therefore goes from 1
  to 2, and an older directory is reported for reinstall.
- A Buzz rule's `GuardDecision.allow` becomes `pass`. At 25b8902a7 that is one mechanical
  rename over 179 sites in 12 files under `hack/policy/`. The generated Buzz type names
  follow the Go moves.
- `guard.Request.DryRun` is deleted. The live lease `fix-small-harness-gaps-in-go`
  holds `cmd/magus/job.go` today, so that edit has to be coordinated with it.

What changes for callers:

- A planner can judge a call as many times as it likes without changing anything. The
  guide's harness judges, then calls `Run` with `WithCallID`, and the trail shows one row
  per call.
- A re-fired hook does no harm on a host that sends a call id. On a host that sends none,
  idempotence depends on the open question below.
- An embedder with an approval UI receives `ask` with its reason. One without an approval
  UI receives a deny that names `WithRendersAsk`.
- A program that never spawns needs no spawn rule.
- The host glues and `GuardSchemaVersion` stay as they are. `GuardTemplateVersion` moves
  only if reading the call id needs a glue change, and a glue that forwards the whole
  envelope needs none.

The implementing change owes these, all outside this record's one file:

- The outer-harness page, corrected to four outcomes.
- The SDK reference's judging section and its "what the SDK leaves out" list, since the
  harness is no longer the only way to declare tools.
- The guide example.
- Rule pages for `unjudged-tool` and the new `external-act` and `spawn-rule-unset`.
- A refusals row for "a dry-run option on judging".
- The review's example `Example_previewACallWithoutSpendingIt`, which flips from `advise`
  then `pass` to `advise` twice.

## Open questions

- A host sends no call id: record without a key and say so on the row (yes), or refuse
  to record (no)?
- Move host reads off the `sed`/`cat` rewrite and onto capability rules in the same
  change (yes or no)?
- Ship `WithToolRule` beside `magus\guard.tool` (both), or only the Buzz seam (Buzz)?
- Name the package `types/agent` or `types/guard`?
- Name the capability `external` or `irreversible`?

## Amendments

### 2026-11-12: the tool door becomes ADR 0046's `Judge`

[ADR 0046](0046-the-judge-takes-any-agent-step.md) renames the tool door to take any
agent step. `JudgeTool` becomes `Judge`, `ToolCall` becomes `Step`, `ToolRow` becomes
`StepRow`, `ToolRequest` becomes `StepRequest`, `WithToolRows` becomes
`WithStepRows`, and `magus\guard.tool` becomes `magus\guard.step`. Go rules stay: an
embedder may register a rule in Go and in Buzz, so `ToolRule` and `WithToolRule` become
`StepRule` and `WithStepRule`, which answers the open question above as "both". The
text above is kept as written.

### 2026-11-12: `types` imports `types/agent`, not the reverse

"Where the names live" says `types/agent` imports `types` and `types` never imports it.
The tree says the opposite. `types` imports `types/agent` (`types/job.go`,
`types/harness_contract.go`). `types/agent` imports nothing in magus but
`internal/json` and `types/enum`, and the compiler refuses the cycle an import of `types`
would make. So `types/agent` is the leaf, and it cannot name `types.NextStep`,
`types.LeaseSource`, `types.GuardProfile` or `types.ModelTier`.

What follows for the moves above:

- A type moves into `types/agent` only with every `types` name it reads, or it waits.
  `Judgment` and `Verdict` cannot embed `types.GuardJudgment` or `types.GuardVerdict`
  from the leaf. `Magus.Judge` returns `types.GuardJudgment`, and a `StepRule` returns
  `types.GuardVerdict`, until those move.
- `AgentFacts`, `RuleDoc` and `HarnessTools` need not move to keep `types` free of an
  import cycle, since `types` may already import the leaf.
- `Capability` moves with its values. Until then a `StepRow` holds
  `types.ToolCapability` values as strings.

The text above is kept as written.
