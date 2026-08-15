# Concepts: what deepseek-harness knows that we should

Written 2026-08-15. Rationale and transferable ideas. The contractual work list
is [delegation-surface-implementation-plan.md](delegation-surface-implementation-plan.md);
nothing here is a work item.

This file is meant to be reread when starting something adjacent. Several of
these principles are about delegation only by accident.

## The source

`deepseek-ai/deepseek-harness` is an agent harness, not a model SDK: a peer of
Claude Code and Codex with its own TUI, web UI, sessions, sandbox, approvals,
and subagents, built on the Cordis plugin framework. It ships `.agents/notes/`,
roughly sixty dated decision notes in Problem / Decision / Alternatives
considered / Consequences form.

Nine were read:

- `feature/2026-07-25-subagent-policy-inheritance.md`
- `feature/2026-07-12-subagent-persona-tool-filter-and-depth.md`
- `feature/2026-08-09-parallel-subagent-delegations.md`
- `feature/2026-08-11-background-first-continuable-delegation.md`
- `feature/2026-07-30-continuable-subagent-report-tool.md`
- `feature/2026-07-10-parallel-tool-call-execution.md`
- `architecture/2026-07-08-agent-scope-contexts.md`
- `architecture/2026-07-24-separate-context-injection-from-turn-execution.md`
- `architecture/2026-08-11-trajectory-conversation-context-assembly.md`

They are one layer below us. They own the runtime; our skill is doctrine running
as a guest inside someone else's runtime. That asymmetry is what makes the notes
useful rather than redundant: they had to make every mechanism real, so their
rejected alternatives are load-bearing in a way a design doc's usually are not.

## The constraint that shapes everything

magus is not building governance. Insight and observability: facts the root
agent can reopen, never permissions it must satisfy.

This already cost one idea in this very session. The first version of the
disjointness work was a command that took N proposed write sets and returned a
fail-closed verdict. A verdict is a gate. The same facts with no judgment are
strictly more useful and strictly less dangerous, because a wrong gate blocks
correct work while wrong evidence merely gets overruled.

Worth applying as a test elsewhere: if a proposed magus surface would make a
caller's work fail, ask whether emitting the fact would have been enough.

## Principles worth keeping

### Visibility is not authority

Their sharpest section. `toolFilter` controls what a child SEES, not what it may
DO, and they say so in a heading rather than burying it. A plugin holding a
context can call services directly; the filter is composition, not confinement.

Ours has the same property and does not admit it. The Forbidden paths column
reads like a boundary and is prompt text. The generalization: any mechanism that
shapes what an agent perceives will be read as constraining what it can do
unless you say otherwise, and the place to say it is a heading.

The corollary is more useful than the warning. Once you admit the prompt is not
the boundary, you have to name what is, and naming it usually promotes some
step you had been treating as bookkeeping. Here it promotes diff review at
integration.

### Snapshot beats live resolution

They rejected walking `parentSession` at each call because a mid-run parent
change would retroactively rewrite a running child. The semantic is: the child
keeps what it was handed; cancel-and-respawn picks up a tightening.

This generalizes past delegation to anything long-running that reads shared
config. The failure it avoids is not a race, it is an explanation problem: a
worker that changed behavior mid-run cannot be reasoned about from either the
old or the new configuration.

Ties directly to VCS. A revision is exactly the reopenable anchor this needs,
and it is the honest form of the observability constraint: a revision grants
nothing and forbids nothing, it just says what was true. Anywhere we hand work
to something that runs for a while, the question "what revision was it handed"
is probably worth being able to answer.

### Subtraction beats restriction

Their three composition controls are independent: persona, tool filter, depth.
Ours collapses two axes into one column, assigning how much thinking a unit gets
and saying nothing about what surface it may touch.

The interesting move is not restricting the surface, it is what restriction
buys: a unit with no write capability has no write set, so it needs no ownership
row and no collision proof. It leaves the analysis rather than being managed
inside it.

Generalizable heuristic: when a classification adds bookkeeping to every case,
look for the case where the bookkeeping can be removed entirely instead of
filled in cheaply.

### Two return paths, one unconditional

A continuable child owes a self-contained final report, AND the manager emits a
settlement notice on every terminal path regardless of whether the child
cooperated. They accept that the two messages may duplicate content, because the
alternative is conditional settlement, which loses the guarantee exactly when a
child reports progress and then dies.

Our acceptance-evidence rule is stronger than their report tool - an output ref
beats prose - but we have only the cooperative path. We handle the worker that
fails twice and not the worker that returns nothing.

The principle: a cooperative channel and a guaranteed channel are different
things, and collapsing them loses the guarantee in precisely the case that
motivated it. Applies to any place we infer success from a report.

### Default to non-blocking, name the blocking test

They flipped their default to background and stated the test positively: wait
only when your next action requires the result. Ours gestures at the same rule
negatively ("do not create agents merely to wait").

Small, but the positive form is actionable and the negative form is a
prohibition you can obey while still getting it wrong.

## The asymmetry we should press

Their parallel tool-call note rejects resource-aware classification explicitly.
A tool may declare only "I overlap with anything else that also said yes"; it
cannot say "safe only when the paths differ", because relational safety
"requires shared resource identity and conflict semantics across unrelated
tools" that a general harness does not have. Their delegation tool therefore
declares itself unconditionally concurrency-safe and hands coordination to the
model. They note Claude Code, opencode, and oh-my-pi all land in the same place.

magus has exactly the shared resource identity they lack: declared outputs,
dependency edges, generated trees, temporal affinity. The engine already
schedules relationally.

So the gap is not magus versus them. It is internal. The delegation layer is the
one place magus stops using its own graph and asks a language model to re-derive
in prose what the engine computes from declarations. Every other magus surface
would call that a guess.

This is the thesis worth carrying into other sessions: wherever an agent is
asked to reason about what touches what, magus should already know, and the
question is only whether the fact is reachable.

## Process idea, unresolved

Their `.agents/notes/` carries a status per note (proposed, implemented,
archived, rejected) and explicit supersedes links between notes: "the
approvals-pinned decision supersedes this note's original inheritance." A
reversed decision stays in the tree with the reversal recorded on it.

MEMORY.md has the same instinct in its "Superseded (kept for reversed
decisions)" line, but the edge lives in a growing index rather than on the notes
themselves. Theirs scales better: the note you happen to open tells you it was
overruled, without needing the index to be read first.

Worth raising against the notes-store work rather than deciding here.

## Out of reach, and worth knowing why

Host-owned. Listing them is not a roadmap, it is a boundary marker for future
sessions tempted to write doctrine that cannot be enforced.

- Policy inheritance stamped into a child's session log as source-tagged events.
- Scoped tool registries and per-child persona shadowing installed before the
  child's first model request.
- Mid-flight child reports that wake a parked parent.
- Depth caps enforced at every start, with the delegation tool deliberately left
  visible so a child receives a rejection rather than silently not knowing. Ours
  is honor-system text.

The shape of that list is the real lesson. Everything a harness enforces
structurally, a skill can only request, and a request that reads like a rule is
the failure mode. When adding to the agent surface, sorting proposals into
"magus can observe this" and "only the host can enforce this" is the first move,
not a late check.
