---
title: "ADR 0046: the judge takes any agent action"
order: 46
description: The Go SDK judges any action an embedder's agent is about to take, not only a shell line, a file write or a tool call. The caller describes the action as data (a kind, a name and an input), declares each kind once, and gets the one verdict the guard already returns. A kind is an open name the embedder chooses, so magus ships no list of them. Built-in rules keep reading capabilities, and the workspace's Buzz policy holds the rules for every kind, including kinds magus never ships. ADR 0039's tool door becomes this door under a wider name, and no second door is added. The rules, the policy load, the verdict and the recording stay one implementation.
tags: [adr, decision, guard, sdk, agents, harness]
status: proposed
date: 2026-11-12
---

# ADR 0046: the judge takes any agent action

## Context

Eli said on 2026-11-12 that the judgment mechanism will be used for far more than shell
commands and file writes. An embedder in an agent framework, or any Go agent loop, must
be able to ask it about any action its agent takes. This record is written before any
code because it changes the root package's exported API. The evidence was read from this
tree at da82588c2 on 2026-11-12.

What the guard judges today, inside `guard.Judge` (`internal/guard/guard.go:750`):

- One `guard.Request` (`internal/guard/guard.go:631`) carries every call. Its payload is
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
  store can supply the row that says which revision may approve a call.

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
returns it. It never performs the action, never suspends or resumes the caller's loop,
never retries and never chooses what happens next. Judging is pure and recording is a
separate step keyed on the action's id.

Parts 1 to 6 are written in neutral terms, with an invented support-desk agent as the
example. Part 7 maps them onto magus.

### 1. An action is data: a kind, a name and an input

The caller describes what its agent is about to do as one record.

- The kind says what sort of action it is: `tool`, `model`, `memory`, `step`, `egress`,
  `message`, `delegate`, or any other name the embedder chooses.
- The name says which one: the tool `refund_order`, the model `small-summarizer`, the
  store `customer-notes`.
- The input is the action's own arguments, as a JSON object: `{"order": 42, "amount":
  10.00}`.
- The envelope around them is typed and the same for every kind: an id, the session, the
  acting agent, the directory, the time, and the task identity the caller acts under.

The judge never receives a restated action. A refund is not rewritten as a shell line,
and a rule reads the fields the caller sent.

### 2. What each action shows the judge, and what its verdict says

| action                           | what the judge must see                                                          | what a verdict must say                                                   |
| -------------------------------- | -------------------------------------------------------------------------------- | ------------------------------------------------------------------------- |
| a tool call with typed arguments | the tool, its arguments, the paths or command among them, its declared effects   | allow, or deny with the rule and the call to make instead                 |
| a model call                     | the model, the provider's host, the purpose, the size of what is sent            | deny when the model is above the task's tier, naming the tier allowed     |
| a memory or state write          | the store, the key, whose scope it is, whether it outlives the session           | deny a write to a scope the task does not own, naming the scope it owns   |
| a workflow step                  | the workflow, the step, the steps already recorded for this run                  | deny a step out of order, naming the step that must be recorded first     |
| network egress                   | the host, the port, the method, whether a body leaves                            | deny a host outside the declared set, naming the set                      |
| a message to a person            | the channel, the recipient, the text, whether this is the first contact          | ask for consent, or deny. Never an edited message                         |
| giving work to another agent     | the receiving agent, its model, the task text, the giver's task, the write paths | deny with the reason and what to declare, by the same rule a spawn passes |

Every verdict is one of pass, advise, deny and ask, with the reason, the rule that
decided and, where a command remedies it, the next step.

### 3. A kind is declared once, and an undeclared kind is refused

The embedder declares each kind of action it will ask about, once, when it opens the
judge. A declaration is a row of data: the kind, the name (empty covers every name of
that kind), the capabilities, and where in the input a built-in rule finds a path, a
command or a spawn's description.

- A row carries locations only for what built-in rules read. Everything else a rule
  reads from the input itself.
- An action no row covers is denied, and the reason names the kind and where to declare
  it.
- A malformed row fails the open: no kind, a repeated kind and name, an unknown
  capability, or a location that does not parse.
- A row that is not `inert`, has no capability a built-in rule judges and has no
  workspace rule registered for its kind is an error when the rules load. The error names
  the kind and the seam. Nothing passes because nobody was asked.
- An input whose declared location holds the wrong type is denied, and the reason names
  the field. The agent can correct that, so it is a verdict and not an error.

### 4. One verdict

No kind gets its own verdict type. The record is ADR 0039's judgment: pass, advise,
deny or ask, with the reason, the context, the rule, the lease, the next steps, the id
and the advice spent. The strictest decision wins.

- An ask goes to a caller that said it can prompt. Otherwise it is a deny that says so
  (ADR 0039, part 5).
- The next step stays a command a person can paste (`types.NextStep`,
  `types/next_step.go:30`). When the remedy is another action of the embedder's, the
  reason names it.
- A verdict never carries a changed action.

### 5. Rules for any kind are workspace policy

A workspace's Buzz policy registers a rule for a kind by name. The rule receives the
action, the row's capabilities, the paths and command read from the input, and the
lease, and it returns a verdict.

- The kind is an open name with the rule-name grammar (`guardRuleName`,
  `types/guard.go:140`). magus keeps no list of kinds, so a kind it never ships is
  written the same way as `tool`.
- The four existing seams keep their names and their typed requests, because they carry
  parsed commands and resolved paths. An embedder's kind may not take one of those names.
- Built-in rules read capabilities, never kinds. A `runs` row's command, a `reads` or
  `writes` row's paths, a `spawns` row and an `external` row are judged as ADR 0039 says,
  whatever the kind is called.
- A rule for a kind is a Go `ActionRule` (an SDK embedder's door) or a Buzz
  `magus\guard.action` function (the workspace's policy). Both feed the one judgment. A
  workspace's Buzz policy is read at the row's revision beside its approved copy (ADR
  0045, part 2); a Go rule is the embedder's own code and has no approved copy.

### 6. What stays one implementation, and what a caller supplies

One implementation: the rules, the policy load, the order rules run in, the strictest
decision, the verdict, the recording and the trail. Every door computes the same verdict
for the same action against the same state.

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
type Action struct { // replaces ADR 0039's ToolCall
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

type ActionRow struct { // replaces ADR 0039's ToolRow
    Kind                  string       `json:"kind"`
    Name                  string       `json:"name,omitempty"`
    Capabilities          []Capability `json:"capabilities"`
    Paths                 []string     `json:"paths,omitempty"`
    Command               string       `json:"command,omitempty"`
    SpawnDescriptionField string       `json:"spawn_description_field,omitempty"`
}

type ActionRequest struct { // replaces ADR 0039's ToolRequest: what a rule reads
    Action       Action
    Capabilities []Capability
    Paths        []string
    Command      string
    Lease        string
}
```

In the root package:

```go
func (m *Magus) Judge(ctx context.Context, action agent.Action) (agent.Judgment, error)
func (m *Magus) RecordJudgment(ctx context.Context, action agent.Action, j agent.Judgment) error
func WithActionRows(rows ...agent.ActionRow) Option
func WithActionRule(rule ActionRule) Option
```

`ActionRule` is
`func(ctx context.Context, req agent.ActionRequest) (agent.Verdict, error)`.

- `Judge` replaces `JudgeTool`, `WithActionRows` replaces `WithToolRows`, and
  `ActionRule` and `WithActionRule` replace ADR 0039's `ToolRule` and `WithToolRule`, so
  the names match `Judge`, `Action` and `ActionRow`. Against ADR 0039 the root package
  has the same names renamed, none dropped and none new.
- A rule has two doors that feed one judgment and one verdict. `WithActionRule` is the
  SDK embedder's door: a Go program registers a rule without writing a Buzz policy. A
  workspace's own policy stays in its Buzz, beside `magus\guard.action`. The strictest
  decision wins across both, as it does across the built-in rules.
- `Dir` is required when the row has `Paths` or a `Command`, and an empty one is then an
  error that names the field.
- In Buzz, `magus\guard.action(<kind>, <fun>)` replaces ADR 0039's `magus\guard.tool`.
  The Buzz mirrors are generated
  ([ADR 0035](0035-buzz-bindings-come-from-one-config-driven-generator.md)).
- The hook's envelope decodes into the same record: kind `tool`, `tool_name` as the name
  and `tool_input` as the input. `unjudged-tool` becomes `unjudged-action`.
- `magus shell` reads the same record as JSON, so a caller that is not Go reaches the
  same judgment.
- ADR 0045's `magus.WithJobReader` applies to `Judge` unchanged.

### State

| item                                                                         | State     |
| ---------------------------------------------------------------------------- | --------- |
| `JudgeCommand`, `JudgeWrite` (`judge.go:25`, `:31`)                          | done      |
| workspace rule seams `command`, `spawn`, `write`, `stop`                     | done      |
| `agent.Action`, `agent.ActionRow`, `agent.ActionRequest`                     | proposed  |
| `Magus.Judge`, `WithActionRows`; `RecordJudgment` takes an `Action`          | proposed  |
| `magus\guard.action(<kind>, <fun>)`                                          | proposed  |
| `ActionRule`, `WithActionRule`, beside the Buzz seam                         | proposed  |
| an uncovered kind is an error when the rules load                            | proposed  |
| `unjudged-action` replaces `unjudged-tool`; a mistyped location is a deny    | proposed  |
| the hook envelope and `magus shell` decode into an `Action`                  | proposed  |
| a typed Go struct or method per kind                                         | not built |
| a verdict that changes the action, or a step that suspends the caller's loop | not built |
| judging a result after the action ran                                        | not built |

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
  failure removed is a rule guessing an action's meaning from a tool's name. The other
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
  `Action.At`, and recording is keyed on `Action.ID`.

## Alternatives

### A typed Go struct and a method for each kind

`JudgeModelCall(ModelCall)`, `JudgeMemoryWrite(MemoryWrite)` and so on. It lost because
the exported API grows with every kind, and a kind magus does not ship has no door.
Evidence: the SDK has two doors for two kinds today, and ADR 0039 already replaces them
with one. Reopen when the rules written for one kind, in two or more workspaces, misread
the same field of its input.

### Keep ADR 0039's tool door and declare every action as a tool

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
workspace's Buzz policy give different verdicts for one action, shown by a test.

### The rules behind one interface

A single rule interface that a Go function and a Buzz function both implement. It lost
because the two doors take different inputs, a Go struct and a generated Buzz mirror, and
the interface adds a layer nothing else calls. Reopen when a third door appears.

### A closed list of kinds shipped by magus

It lost because the embedder knows its actions and magus does not, and the shipped Go
stays free of any one domain. Evidence: every built-in rule in ADR 0039 reads a
capability, and none needs a kind. Reopen when a built-in rule needs a fact that no
capability can state.

### A verdict that edits the action

A rule would redact a message or change an argument on the way through. It lost to
[The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md):
the transcript would no longer say what the agent did. This stays closed while that page
stands.

### magus holds the action until a person approves

It lost because holding and resuming is a loop, and the loop is the embedder's
([refusals](../design/refusals.md), line 61). The ask verdict gives the embedder what it
needs to hold the action itself. Reopen only if that refusal is reopened.

## Consequences

- ADR 0039's `JudgeTool`, `ToolCall`, `ToolRow`, `ToolRequest`, `WithToolRows`,
  `ToolRule`, `WithToolRule` and `magus\guard.tool` are renamed before they are built.
  The Go rule becomes `ActionRule` and `WithActionRule`. ADR 0039 needs a dated amendment that points here.
- ADR 0045, part 8, says `JudgeCommand` and `JudgeWrite` keep their signatures. ADR 0039
  deletes them. Part 8 should name `Judge`.
- The rule `unjudged-tool` is renamed on the hook path too, which changes a rule name on
  the trail. That break is deliberate.
- An embedder writes a Go rule or a Buzz rule to judge its own kinds. A program that
  declares only `inert` rows and rows the built-in rules judge writes none.
- A rule for a kind reads an untyped input. A field it misreads is found when the rule
  runs, not when the policy is checked.
- The implementing change owes the SDK reference and guide, the rule page for
  `unjudged-action` and a refusals line for each `not built` row above.

## Open questions

- May a row name a schema for its input, checked before any rule runs (yes), or does the
  rule read an untyped input until a misread field is measured (no)?
- Does a rule read the actions already recorded for a session through a typed repository
  ([ADR 0030](0030-buzz-reads-state-through-typed-repositories.md))? A workflow-step rule
  needs it.
- Does a recorded ask carry what the person answered (yes or no)?
- Does a deny carry a typed next action beside `types.NextStep`'s command (yes), or does
  the reason name it (no)?
- May an embedder's loop ask the `stop` seam whether its agent may end a turn (yes or
  no)?
- A caller that is not Go judges through `magus shell`, one process per action. Is that
  cost acceptable for an embedder in another language, measured against the hook's
  seven-second budget?
- Is the hook path's kind named `tool`, or does each host tool keep only its name?

## Amendments
