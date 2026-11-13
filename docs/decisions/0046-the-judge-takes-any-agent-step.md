---
title: "ADR 0046: the judge takes any agent step"
order: 46
description: The Go SDK judges any step an embedder's agent is about to take, not only a shell line, a file write or a tool call. The caller describes the step as data (a kind, a name and an input), declares each kind once, and gets the one verdict the guard already returns. A kind is an open name the embedder chooses, so magus ships no list of them. Built-in rules keep reading capabilities, and the workspace's Buzz policy holds the rules for every kind, including kinds magus never ships. ADR 0039's tool door becomes this door under a wider name, and no second door is added. The rules, the policy load, the verdict and the recording stay one implementation.
tags: [adr, decision, guard, sdk, agents, harness]
status: proposed
date: 2026-11-12
---

# ADR 0046: the judge takes any agent step

## Context

Eli said on 2026-11-12 that the judgment mechanism serves far more than shell
commands and file writes. An embedder in an agent framework, or any Go agent loop, must
be able to ask it about any step its agent takes. This record is written before any
code because it changes the root package's exported API. The evidence was read from this
tree at da82588c2 on 2026-11-12.

What the guard judges today, inside `guard.Judge` (`internal/guard/guard.go:750`):

- One `guard.Request` (`internal/guard/guard.go:631`) carries every step. Its payload is
  one string, `Input`, and a flag, `IsPath`, says whether that string is a path.
- A shell line is the default reading of `Input`. A path write is `IsPath`, or a host
  envelope that names a write (`:907`).
- A spawn or a continuation is read from the host envelope and judged by
  `judgeAgentEvent` (`:910-915`), which hands a `types.SpawnRequest`
  (`types/spawn.go:62`) to the spawn rules.
- An MCP tool call is read from the envelope's `tool_name` and `tool_input`
  (`hookEnvelope`, `:1856`) and carried as `mcpTool` (`:888`). A tool the host's tool rows
  do not list is refused as `unjudged-tool` (`:892`).
- A stop is an envelope with nothing to judge, graded by `judgeStop` (`:861`).
- A prompt the person typed is `Request.Message` (`:770`). Nothing is judged, and its
  topics are recorded for later rules.
- The result is `types.GuardJudgment` (`types/guard_judgment.go:8`): a decision, a
  reason, a context, the rule that decided, the lease it was graded under and the next
  steps.
- Workspace rules attach at four seams, `command`, `spawn`, `write` and `stop`
  (`reservedGuardRuleNames`, `types/guard.go:135`). Each returns a `types.GuardVerdict`
  (`types/guard.go:123`).

What the SDK offers today, in `judge.go`:

- `JudgeCommand` (`judge.go:25`) and `JudgeWrite` (`judge.go:31`) each build a
  `guard.Request` from one string.
- `judge` (`judge.go:35`) reads the lease from the context (`types.WithJobLease`,
  `types/lease_context.go:15`), builds `guardDependencies` (`judge.go:45`) and calls
  `guard.Judge`.
- [ADR 0039](0039-judging-is-a-pure-computation.md) proposes one door for an embedder's
  tools, `JudgeTool`, over tool rows declared at `Open`. None of it is built:
  `./magus refs JudgeTool` answers "absent" for the symbol on this tree.
- [ADR 0045](0045-a-worker-is-judged-by-its-checkpoint.md) proposes
  a row reader in `types` and `magus.WithJobReader`, so an embedder with no magus job
  store can supply the row that says which revision may approve a step.

The gap: ADR 0039's door judges a tool call and nothing else. An agent in an embedder's
loop also calls models, writes memory, advances a workflow, reaches the network, messages
people and gives work to other agents. Under ADR 0039 each of those has to be declared as
a tool whose name encodes what it is, and a rule has to recover the meaning from the
name.

Two other frameworks' designs were read as input, from their public documentation
before this session, and were not fetched again on 2026-11-12. Mastra lets a tool require
approval, which suspends the run until a person answers, and runs processors over a
model's input and output that may change or stop a message. The OpenAI Agents SDK runs
guardrails over an agent's input and output that stop the run. Nothing below is justified
by them, and the Alternatives say where this record declines what they do.

## Decision

Unchanged from ADR 0039: the SDK has no agent loop and no model router
([refusals](../design/refusals.md), lines 61 and 62). magus computes a verdict and
returns it. It never performs the step, never suspends or resumes the caller's loop,
never retries and never chooses what happens next. Judging is pure and recording is a
separate step keyed on the step's id.

Parts 1 to 6 are written in neutral terms, with an invented support-desk agent as the
example. Part 7 maps them onto magus.

### 1. A step is data: a kind, a name and an input

The caller describes what its agent is about to do as one record. Three levels of work
carry three names, and the judge rules on the lowest:

- A job is the unit of work ([ADR 0044](0044-a-job-may-carry-an-agent.md)).
- An attempt is one try at a job.
- A step is one thing the agent does during an attempt, and the judge rules on it. A step
  need not be a tool call, and the word implies no sequence: two steps may overlap or
  never repeat. A target's build step (`internal/cache.Step`) shares the word, and the package says which is meant.

- The kind says what sort of step it is: `tool`, `model`, `memory`, `workflow`, `egress`,
  `message`, `delegate`, or any other name the embedder chooses.
- The name says which one: the tool `refund_order`, the model `small-summarizer`, the
  store `customer-notes`.
- The input is the step's own arguments, as a JSON object: `{"order": 42, "amount":
  10.00}`.
- The envelope around them is typed and the same for every kind: an id, the session, the
  acting agent, the directory, the time, and the task identity the caller acts under.

The judge never receives a restated step. A refund is not rewritten as a shell line,
and a rule reads the fields the caller sent.

### 2. What each step shows the judge, and what its verdict says

| step                             | what the judge must see                                                          | what a verdict must say                                                   |
| -------------------------------- | -------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| a tool call with typed arguments | the tool, its arguments, the paths or command among them, its declared effects   | allow, or deny with the rule and the step to take instead                 |
| a model call                     | the model, the provider's host, the purpose, the size of what is sent            | deny when the model is above the task's tier, naming the tier allowed     |
| a memory or state write          | the store, the key, whose scope it is, whether it outlives the session           | deny a write to a scope the task does not own, naming the scope it owns   |
| a workflow step                  | the workflow, the step, the steps already recorded for this run                  | deny a step out of order, naming the step that must be recorded first     |
| network egress                   | the host, the port, the method, whether a body leaves                            | deny a host outside the declared set, naming the set                      |
| a message to a person            | the channel, the recipient, the text, whether this is the first contact          | ask for consent, or deny. Never an edited message                         |
| giving work to another agent     | the receiving agent, its model, the task text, the giver's task, the write paths | deny with the reason and what to declare, by the same rule a spawn passes |

Every verdict is one of pass, advise, deny and ask, with the reason, the rule that
decided and, where a command remedies it, the next step.

### 3. A kind is declared once, and an undeclared kind is refused

The embedder declares each kind of step it asks about, once, when it opens the
judge. A declaration is a row of data: the kind, the name (empty covers every name of
that kind), the capabilities, and where in the input a built-in rule finds a path, a
command or a spawn's description.

- A row carries locations only for what built-in rules read. Everything else a rule
  reads from the input itself.
- A step no row covers is denied, and the reason names the kind and where to declare
  it.
- A malformed row fails the open: no kind, a repeated kind and name, an unknown
  capability, or a location that does not parse.
- A row that is not `inert`, has no capability a built-in rule judges and has no
  workspace rule registered for its kind is an error when the rules load. The error names
  the kind and the seam. Nothing passes because nobody was asked.
- An input whose declared location holds the wrong type is denied, and the reason names
  the field. The agent can correct that, so it is a verdict and not an error.
- A row may declare a JSON Schema its input must match. Input that does not match is
  refused with the schema path of the first mismatch, before any rule runs, so no rule
  reads a malformed input. A row with no schema leaves the input untyped, and a rule
  reads it as sent. A schema that does not parse fails the open.

### 4. One verdict

No kind gets its own verdict type. The record is ADR 0039's judgment: pass, advise,
deny or ask, with the reason, the context, the rule, the lease, the next steps, the id
and the advice spent. The strictest decision wins.

- An ask goes to a caller that said it can prompt. Otherwise it is a deny that says so
  (ADR 0039, part 5).
- What a deny suggests next is the same primitive it judged: `NextSteps []agent.Step` on the
  verdict. An embedder's loop can pass a suggestion straight back to `Judge`, and the
  deny's reason says why it is offered. A hook renders a next step of the shell kind as
  its command line, so a person still gets the command. `types.NextStep`
  (`types/next_step.go:30`) stays the general breadcrumb CLI results print and gains no
  agent field (Eli, 2026-11-12). The judge only names the step. It never takes it, and
  the loop decides whether to offer it.
- A verdict never carries a changed step.

### 5. Rules for any kind are workspace policy

A workspace's Buzz policy registers a rule for a kind by name. The rule receives the
step, the row's capabilities, the paths and command read from the input, and the
lease, and it returns a verdict.

- The kind is an open name with the rule-name grammar (`guardRuleName`,
  `types/guard.go:140`). magus keeps no list of kinds, so a kind it never ships is
  written the same way as `tool`.
- The four existing seams keep their names and their typed requests, because they carry
  parsed commands and resolved paths. An embedder's kind may not take one of those names.
- Built-in rules read capabilities, never kinds. A `runs` row's command, a `reads` or
  `writes` row's paths, a `spawns` row and an `external` row are judged as ADR 0039 says,
  whatever the kind is called.
- A rule for a kind is a Go `StepRule` (an SDK embedder's door) or a Buzz
  `magus\guard.step` function (the workspace's policy). Both feed the one judgment. A
  workspace's Buzz policy is read at the row's revision beside its approved copy (ADR
  0045, part 2); a Go rule is the embedder's own code and has no approved copy.

### 6. What stays one implementation, and what a caller supplies

One implementation: the rules, the policy load, the order rules run in, the strictest
decision, the verdict, the recording and the trail. Every door computes the same verdict
for the same step against the same state.

A caller supplies data and two answers: its rows, the row reader (ADR 0045's
`magus.WithJobReader`) and whether it can put an ask in front of a person. The lease
still travels in the context. It may also supply rules: a Go rule from an SDK embedder
and a Buzz rule from the workspace feed the same judgment and so the same verdict
(part 7).

### 7. Mapped onto magus

This amends ADR 0039's parts 3 and 4 and its names. That record is proposed and unbuilt,
so no built name breaks.

In `types/agent`:

```go
type Step struct { // replaces ADR 0039's ToolCall
    APIVersion types.APIVersion `json:"api_version"`
    ID         string           `json:"id"`
    Kind       string           `json:"kind"`
    Name       string           `json:"name"`
    Input      map[string]any   `json:"input"`
    Session    string           `json:"session,omitempty"`
    Agent      string           `json:"agent,omitempty"` // as guard.Request.Agent
    Dir        string           `json:"dir,omitempty"`
    At         time.Time        `json:"at"`
}

type StepRow struct { // replaces ADR 0039's ToolRow
    Kind                  string       `json:"kind"`
    Name                  string       `json:"name,omitempty"`
    Capabilities          []Capability `json:"capabilities"`
    Paths                 []string     `json:"paths,omitempty"`
    Command               string       `json:"command,omitempty"`
    SpawnDescriptionField string       `json:"spawn_description_field,omitempty"`
    InputSchema           []byte       `json:"input_schema,omitempty"` // JSON Schema; unset leaves the input untyped
}

type StepRequest struct { // replaces ADR 0039's ToolRequest: what a rule reads
    Step         Step
    Capabilities []Capability
    Paths        []string
    Command      string
    Lease        string
}

```

`agent.Verdict` and `agent.Judgment` carry what a deny suggests as `NextSteps []agent.Step`.

In the root package:

```go
func (m *Magus) Judge(ctx context.Context, step agent.Step) (agent.Judgment, error)
func (m *Magus) RecordJudgment(ctx context.Context, step agent.Step, j agent.Judgment) error
func WithStepRows(rows ...agent.StepRow) Option
func WithStepRule(rule StepRule) Option
```

`StepRule` is
`func(ctx context.Context, req agent.StepRequest) (agent.Verdict, error)`.

- `Judge` replaces `JudgeTool`, `WithStepRows` replaces `WithToolRows`, and
  `StepRule` and `WithStepRule` replace ADR 0039's `ToolRule` and `WithToolRule`, so
  the names match `Judge`, `Step` and `StepRow`. Against ADR 0039 the root package
  has the same names renamed, none dropped and none new.
- A rule has two doors that feed one judgment and one verdict. `WithStepRule` is the
  SDK embedder's door: a Go program registers a rule without writing a Buzz policy. A
  workspace's own policy stays in its Buzz, beside `magus\guard.step`. The strictest
  decision wins across both, as it does across the built-in rules.
- `Dir` is required when the row has `Paths` or a `Command`, and an empty one is then an
  error that names the field.
- In Buzz, `magus\guard.step(<kind>, <fun>)` replaces ADR 0039's `magus\guard.tool`.
  The Buzz mirrors are generated
  ([ADR 0035](0035-buzz-bindings-come-from-one-config-driven-generator.md)).
- The hook's envelope decodes into the same record: kind `tool`, `tool_name` as the name
  and `tool_input` as the input. `unjudged-tool` becomes `unjudged-step`.
- `magus shell` reads the same record as JSON, so a caller that is not Go reaches the
  same judgment.
- ADR 0045's `magus.WithJobReader` applies to `Judge` unchanged.

### State

| item                                                                         | State     |
| ---------------------------------------------------------------------------- | --------- |
| `JudgeCommand`, `JudgeWrite` (`judge.go:25`, `:31`)                          | done      |
| workspace rule seams `command`, `spawn`, `write`, `stop`                     | done      |
| `agent.Step`, `agent.StepRow`, `agent.StepRequest`                           | done      |
| `Magus.Judge`, `WithStepRows`                                                | done      |
| `RecordJudgment` takes a `Step`                                              | proposed  |
| `magus\guard.step(<kind>, <fun>)`                                            | done      |
| `StepRule`, `WithStepRule`                                                   | done      |
| an uncovered kind is an error when the rules load                            | done      |
| a mistyped location is a deny (`step-input`)                                 | done      |
| `unjudged-step` replaces `unjudged-tool`                                     | done      |
| the hook and `magus shell` read a `Step` record; a `tool` step is judged as the envelope it stands for | done      |
| a host's own envelope handed to a step rule as a `Step`                      | proposed  |
| a `spawns` row judged by the spawn rules                                     | done      |
| `StepRow.InputSchema`; a mismatch is refused with the schema path            | done      |
| `NextSteps []agent.Step` on the verdict: a deny suggests the step it judged  | proposed  |
| `types.GuardJudgment.Next` (JSON `next`) becomes `NextSteps` (`next_steps`): a deliberate break | done      |
| a typed Go struct or method per kind                                         | not built |
| a verdict that changes the step, or a step that suspends the caller's loop   | not built |
| judging a result after the step ran                                          | not built |

### Principles checked

- [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
  the verdict allows, advises, denies or asks, and carries no changed message, argument
  or prompt. The embedder's loop stays the embedder's.
- [The harness belongs in the task runner](../design/principles/the-harness-sits-on-the-tool-layer.md)
  and the refusals for an agent loop and a model router: magus is a deterministic
  substrate around someone else's loop. `Judge` returns and does nothing else.
- [What we do not build is the product](../design/principles/what-we-do-not-build.md) and
  [Delete before you add](../design/principles/delete-before-you-add.md): one door is
  renamed, and no door is added per kind.
- [Capability is not a reason](../design/principles/capability-is-not-a-reason.md): the
  failure removed is a rule guessing a step's meaning from a tool's name. The other
  frameworks are input only.
- [One source, every door](../design/principles/one-source-every-door.md): the hook, the
  CLI, the SDK and Buzz reach one `guard.Judge` with one record.
- [Abstract at the right layer, or not at all](../design/principles/abstract-at-the-right-layer.md):
  the only interface stays ADR 0045's row reader. Rows are data, and the rules are not
  behind an interface: a Go rule is a function option.
- [Behavior is code, facts are data](../design/principles/behavior-is-code-facts-are-data.md):
  a row is data and a rule is code, Go from an embedder or Buzz from a workspace. The
  shipped Go names no kind, no tool and no
  language.
- [No silent defaults](../design/judgment/no-silent-defaults.md): an undeclared kind is
  denied and an uncovered kind is an error. `inert` is the only way to say nothing judges
  a row.
- [Told, never guessed](../design/judgment/told-never-guessed.md): the caller states the
  kind. Nothing infers it from a name.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md) and
  [ADR 0013](0013-the-guard-denies.md): every deny names its rule and its remedy.
- [The Go proverbs, beyond Go](../design/principles/the-go-proverbs.md): a deny is a
  judgment with a nil error. The error is for a judgment that could not be made.
- [Break now, hold still later](../design/principles/break-now-hold-still-later.md): the
  renames carry no alias and no second reader.
- [ADR 0039](0039-judging-is-a-pure-computation.md): `Judge` writes nothing, the clock is
  `Step.At`, and recording is keyed on `Step.ID`.

## Alternatives

### Judge an action, not a step

The first draft named the judged unit an Action. It lost because the word says nothing
about where the unit sits: a job, an attempt and the thing done inside it are all
actions. A Step is the lowest level, one thing the agent does during an attempt, and it
need not be a tool call. Reopen if a unit arrives that no embedder can call a step.

### Judge a call

The second draft named the unit a Call, because Mastra's own word for the intercepted
unit is a tool call. It lost on Eli's call of 2026-11-12: step reads as what an agent
takes next, which is what the judge rules on and what a deny suggests. The word is
shared with a target's build step (`internal/cache.Step`, `types.ChainStep`), and the
package name says which one is meant. Call stays the word for a tool call, a kind of
step. Reopen if a rule or a log must tell the two meanings apart by more than their
package.


### A separate type for what a deny suggests

Drafts gave a deny its own `agent.NextCall`, then an `agent.NextStep` wrapping
`types.NextStep` with a step. Both lost on Eli's call of 2026-11-12: a suggested next
move is another step the agent could take, so it is the same primitive the judge rules
on, and a loop feeds it back without converting it. Adding the step to `types.NextStep`
lost as well, because that type is general, shared by hints, reports, logs, query output
and guard denials. Reopen it if a suggestion ever needs data a step cannot carry.

### A typed Go struct and a method for each kind

`JudgeModelCall(ModelCall)`, `JudgeMemoryWrite(MemoryWrite)` and so on. It lost because
the exported API grows with every kind, and a kind magus does not ship has no door.
Evidence: the SDK has two doors for two kinds today, and ADR 0039 already replaces them
with one. Reopen when the rules written for one kind, in two or more workspaces, misread
the same field of its input.

### Keep ADR 0039's tool door and declare every step as a tool

An embedder would name a tool `model:small-summarizer`. It lost because the meaning sits
in a string a rule has to split, one rule cannot cover every model call, and the trail
cannot group by kind. Evidence: `ToolRow` keys on a name alone. Reopen if, after this is
built, no workspace registers a rule for a kind other than `tool`. Then the kind field is
unused, and it goes.

### Buzz only, with no Go rule function

The first draft dropped ADR 0039's `WithToolRule` so that every rule was Buzz. It lost on
Eli's call of 2026-11-12: an embedder may register a judging rule in Go and in Buzz. The
cost is that policy has two homes, and a Go rule is not read at the row's revision and
has no approved copy, so ADR 0045's guarantee does not cover it. That is accepted: a Go
rule is the SDK embedder's own code, and a workspace's policy stays in its Buzz.
Evidence: `guardDependencies` already fails when the Buzz rules do not load
(`judge.go:45`), so an SDK caller links the engine today. Reopen when a Go rule and the
workspace's Buzz policy give different verdicts for one step, shown by a test.

### The rules behind one interface

A single rule interface that a Go function and a Buzz function both implement. It lost
because the two doors take different inputs, a Go struct and a generated Buzz mirror, and
the interface adds a layer nothing else calls. Reopen when a third door appears.

### A closed list of kinds shipped by magus

It lost because the embedder knows its steps and magus does not, and the shipped Go
stays free of any one domain. Evidence: every built-in rule in ADR 0039 reads a
capability, and none needs a kind. Reopen when a built-in rule needs a fact that no
capability can state.

### A verdict that edits the step

A rule would redact a message or change an argument on the way through. It lost to
[The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
the transcript would no longer say what the agent did. This stays closed while that page
stands.

### magus holds the step until a person approves

It lost because holding and resuming is a loop, and the loop is the embedder's
([refusals](../design/refusals.md), line 61). The ask verdict gives the embedder what it
needs to hold the step itself. Reopen only if that refusal is reopened.

## Consequences

- ADR 0039's `JudgeTool`, `ToolCall`, `ToolRow`, `ToolRequest`, `WithToolRows`,
  `ToolRule`, `WithToolRule` and `magus\guard.tool` are renamed before they are built.
  The Go rule becomes `StepRule` and `WithStepRule`. ADR 0039 needs a dated amendment that points here.
- ADR 0045, part 8, says `JudgeCommand` and `JudgeWrite` keep their signatures. ADR 0039
  deletes them. Part 8 should name `Judge`.
- The rule `unjudged-tool` is renamed on the hook path too, which changes a rule name on
  the trail. That break is deliberate.
- An embedder writes a Go rule or a Buzz rule to judge its own kinds. A program that
  declares only `inert` rows and rows the built-in rules judge writes none.
- A rule for a kind whose row declares no schema reads an untyped input. A field it
  misreads is found when the rule runs, not when the policy is checked.
- The implementing change owes the SDK reference and guide, the rule page for
  `unjudged-step` and a refusals line for each `not built` row above.

## Open questions

- Does a rule read the steps already recorded for a session through a typed repository
  ([ADR 0030](0030-buzz-reads-state-through-typed-repositories.md))? A workflow-step rule
  needs it.
- Does a recorded ask carry what the person answered (yes or no)?
- May an embedder's loop ask the `stop` seam whether its agent may end a turn (yes or
  no)?
- A caller that is not Go judges through `magus shell`, one process per step. Is that
  cost acceptable for an embedder in another language, measured against the hook's
  seven-second budget?
- Is the hook path's kind named `tool`, or does each host tool keep only its name?

## Amendments

### 2026-11-12: the Go door is built; the package points the other way

`types` imports `types/agent` (`types/job.go`, `types/harness_contract.go`), so
`types/agent` is the leaf, not the package above `types` that ADR 0039 assumed. What
was built follows from that:

- `types/agent` imports nothing in magus but `internal/json` and `types/enum`, and an
  import of `types` would be a cycle. So `Step.APIVersion` is a string,
  `magus.step/v1alpha1`, and `StepRow.Capabilities` holds `types.ToolCapability`
  values as strings. The named type moves into `types/agent` with ADR 0039's capability
  move.
- `Judge` returns `types.GuardJudgment` and a `StepRule` returns `types.GuardVerdict`.
  `agent.Judgment` and `agent.Verdict` cannot embed them from the leaf, so they wait on
  ADR 0039's moves.
- A spawns row fails the open: a spawn is still judged only on the hook path.
- A Go rule's verdict reports under `step:<rule>`, or `step:<kind>` when it names none.

The rows left proposed, and why:

- `RecordJudgment` waits on ADR 0039 part 2. Until then `Judge` records on the trail
  as `JudgeCommand` does.
- `magus\guard.step` needs a registry seam, a generated `StepRequest` mirror (ADR 0035)
  and the approved-copy load, which is its own change.
- `unjudged-step` renames a rule the shipped harness spells, `hack/policy` and the
  guides name. `Judge` denies an undeclared kind under `unjudged-tool` until then.
- The hook envelope and `magus shell` keep their shape-first decode until the rename
  and the Buzz seam land.
- `NextSteps []agent.Step` can sit on `types.GuardJudgment`, since `types` imports the
  leaf. The guard's remedy rules read `types.NextStep` throughout, and moving them is
  its own change.

### 2026-11-12: the hook, the CLI and spawns reach `JudgeStep`

What was built:

- The rule is `unjudged-step` on every door. It never shipped as `unjudged-tool` in a
  release, so the rename breaks only a pre-release `magus\guard.builtins` that names the
  old key. That load now fails with MGS1045.
- `guard.Judge` reads a `Step` record wherever it reads a host's envelope, so the hook
  and `magus shell` take the record as JSON. The kind a host's tool call is, is `tool`,
  which answers the last open question. The host's tool rows declare that kind. So a
  `tool` step no `StepRow` covers is judged as the envelope it stands for: the name is
  `tool_name`, the input is `tool_input`, and `Dir` is `cwd`. Every other record goes to
  `guard.JudgeStep`. A record it cannot judge, such as one with another `api_version`,
  is denied under `step-input` and never cleared, since a hook fails open on an error.
- A `spawns` row no longer fails the open. Its step is judged as a host's spawn, by the
  same built-in spawn rules and `magus\guard.spawn`. Its input needs a prompt.
  `SpawnDescriptionField` is a JSON Pointer to the description, and it is refused on a
  row that does not spawn. A spawns row shares no row with `runs`, `reads` or `writes`.
  As on the hook path, judging a spawn records it.

Still proposed, and why:

- `magus\guard.step` needs a step rule kept on `workspace.WorkspaceRegistry`, beside the
  four seams. The live lease `workspace-imports-fit-their-ceiling` holds that file to take
  `types/agent` out of `internal/workspace`'s imports, and a `StepRequest` rule there
  adds it back. The seam also needs a `StepRequest` mirror from buzzgen, the policy
  snapshot's record of which seams are registered, the approved-copy load, and
  `ForeignJudgeDeps` dropping the rule. Until it lands, a kind other than `tool` sent to
  `magus shell` has no rows and is denied under `unjudged-step`.
- A host's own envelope is not handed to a rule as a `Step` until a rule on the hook path
  reads one. That is `magus\guard.step`.
- `NextSteps []agent.Step` on `types.GuardJudgment` needs every remedy the guard serves
  (`shapeDeny`, `servedNext`, `hint.FilterServable`, the `next_steps` the glues print) to
  carry a step of kind `tool` with the command as its input. Nothing in this change
  needed it.

### 2026-11-12: `magus\guard.step` is built

What was built:

- `magus\guard.step(<kind>, <fun>)` registers one rule per kind in the root magusfile.
  The kind takes the rule-name grammar. `step` joins the reserved names, since an
  unnamed verdict reports as `workspace:step`. A named one reports as
  `workspace:<rule>`, as the other seams do.
- The rules sit on a `steprule.Set` (`internal/guard/steprule`), not on
  `workspace.WorkspaceRegistry`, so `internal/workspace` still does not import
  `types/agent`. A load puts the set on its context as it puts the model tiers: `Magus`
  holds its own, and each guard load holds one beside its registry. The registry
  still releases the session a rule runs on.
- `StepRequest` and `Step` (`AgentStep` in Buzz) are generated mirrors (ADR 0035).
- `JudgeStep` asks the rule for the step's kind after the Go rules, on the working
  tree and the approved copy (ADR 0045), and keeps the stricter answer. A rule that
  fails judges nothing and says so beside the verdict, as the stop seam does. A step
  that changes state is denied when the rule runs out of time.
- A row with no built-in capability and no Go rule is checked when the magusfile
  loads, not when the rows are read. It opens when the root magusfile registers a
  rule for its kind. At judgment a step only that rule judges is denied under
  `unjudged-step` when no side answers.
- The policy snapshot records the kinds each side registers, and the trail's marker
  records them too. A removed kind counts as a pending loosening. `ForeignJudgeDeps`
  drops both sides of the rule.
- `WithStepRows`, `WithStepRule` and `WithSpawnRule` panic on a bare `workspace.Load`
  rather than set nothing.

Still proposed: a host's own envelope is handed to no step rule. A `tool` step no row
covers is still judged as its envelope, so `magus\guard.step("tool", ...)` judges only
the tool steps a row declares.
