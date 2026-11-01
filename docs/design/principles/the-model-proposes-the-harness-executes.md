---
title: The model proposes, the harness executes
order: 34
description: A model emits text that proposes an action; a harness runs the tool, reads and writes the files, refuses the call that breaks a rule and keeps the state. Harnesses come in layers, and the layers compose.
tags: [design, principle, harness, agents]
---

# The model proposes, the harness executes

The word harness gets used for half a dozen things, and most of the confusion
comes from one mistake: crediting the model with work it never does. A model
reads text and writes text. When that text asks for a tool call, something else
parses it, runs the command, writes the file, refuses the call that breaks a
rule and carries the result into the next turn. That something is the harness.
The model proposes; the harness executes what the model proposes.

## The model does the work

People say the model "pulled you up by brute force"
([deadbabe, HN](https://news.ycombinator.com/item?id=50152615)). The model has
no hands and no memory: "LLMs are stateless"
([Simon Willison](https://simonwillison.net/guides/agentic-engineering-patterns/how-coding-agents-work/)).
The harness extracts the tool call, runs it, and sends the conversation back in
full on every turn.

## The harness is the product

People say "codex: OpenAI's agent harness."
([nater5000, HN](https://news.ycombinator.com/item?id=50195141)). A product
ships a harness and a UI around it. Gabriel Chua, at OpenAI, splits Codex into
the model, the harness and the apps you reach it through
([via Simon Willison](https://simonwillison.net/2026/Feb/22/how-i-think-about-codex/)).
The harness is the code between the model and the machine, whatever window it
runs in, and the checks a repository adds around it are no product at all.

## A harness is a thin wrapper or a prompt

People say "GPT wrapper (now called Harness)"
([hek2sch, HN](https://news.ycombinator.com/item?id=48516505)). A prompt is
advice the model may skip. Tool execution, state, and checks that refuse are
what make a harness. Mitchell Hashimoto's fix for a mistake an agent repeats
moves past AGENTS.md text to "Actual, programmed tools."
([My AI adoption journey](https://mitchellh.com/writing/my-ai-adoption-journey)).

## The next model makes it unnecessary

People say "those scaffolds will also just be replaced by the reasoning models"
(Noam Brown, quoted by
[Latent Space AINews](https://www.latent.space/p/ainews-is-harness-engineering-real)).
That holds for scaffolding that makes up for weak reasoning: planning prompts,
retry loops. It does not hold for a step with a right answer, such as whether a
file is generated or a check passed: "Part of harness engineering is figuring
out what can be done deterministically to avoid calling an LLM"
([chickensong, HN](https://news.ycombinator.com/item?id=49655268)).

## A harness is a test rig

People read the older sense: "a collection of stubs and drivers configured to
assist with the testing of an application or component"
([Wikipedia](https://en.wikipedia.org/wiki/Test_harness)), or an eval harness
such as SWE-bench's. An eval harness measures an agent; an agent harness runs
one. Anthropic keeps them apart: "we're evaluating the harness and the model
working together"
([Demystifying evals for AI agents](https://www.anthropic.com/engineering/demystifying-evals-for-ai-agents)).

## Everything that is not the model is one harness

People say "If you're not the model, you're the harness."
([Vivek Trivedy, LangChain](https://langchain.com/blog/the-anatomy-of-an-agent-harness)).
That is right by subtraction, but it merges the host's loop with what a
repository adds around it. Birgitta Bockeler draws them as rings and notes that
the term "means different things depending on the bounded context"
([Harness engineering](https://martinfowler.com/articles/harness-engineering.html)).
Say which layer you mean.

## A harness can drive other agents

People say "The Harness can be as much the brain as the LLM."
([mindcrime, HN](https://news.ycombinator.com/item?id=50152494)). A component
that decides what runs next is an agent loop, whatever it is called. The term
gets "conflated with an agent framework, an SDK, an IDE plugin, or an
orchestrator" ([arXiv 2606.10106](https://arxiv.org/abs/2606.10106)). A harness
fixes the steps that have a right answer and leaves the plan to the model.

## The layers

Harnesses come in layers, listed here from the top, and each does what the one
below it cannot:

1. The person sits on top and directs; the harnesses serve them. They write the
   guard rules, fork the jobs, pick the model tier and review what lands
   ([Human in the loop](human-in-the-loop.md)).
2. The [outer harness](../../glossary/outer-harness.md) is magus:
   deterministic scaffolding at points a person chose. The guard, job leases
   and the job store sit around any loop and never inside one
   ([The outer harness stays outside](../judgment/the-outer-harness-stays-outside.md)).
   It answers from magus's workspace model: the project graph, each target's
   inputs and outputs, and the cache.
3. The [inner harness](../../glossary/inner-harness.md) is the agent loop:
   Claude Code, Codex, Cursor or OpenCode. It owns the turns, the tools, the
   prompt and what reaches the model.
4. The model proposes the next action.

<!--diagram:harness-pyramid-->

The inner harness gives the model tools and a context window, but it has no
record of which file is generated, which path another job holds, or whether a
check passed. The outer harness holds that record and answers from it the same
way every time. Only the person decides what the work is for. Two concerns land
at fixed layers: cost belongs to the person, who chooses the work and the tier
table; security belongs to the outer harness, whose guard judges every call
against declared outputs, leases and VCS state, whichever inner harness made it.

## Records, not a loop over loops

Two inner harnesses can work under one outer harness at the same time, on the
same repository, with the person above both. They never talk to each other, and
they do not need to. The person writes the guard rules and forks the rows; each
one takes a job row with `magus job exec`, reads its terms with
`magus describe job`, sends every call through the guard and files its result
with `magus job exit`. The row, the verdicts and the graph are records any loop
can take and return.

<!--diagram:harness-stack-->

Interop comes from those records, never from a component that drives the loops.
magus calls no model, runs no agent loop and routes no call to a model. An outer
harness that did would become one more inner harness, tuned to the hosts it
drives and broken by their next release.

## Enforced by

- [A record of refusals](../refusals.md) lists a model adapter, an agent loop in
  the Go SDK and a model router as not built.
- The hook contract is keyed by field rather than host name, which the
  `hostagnostic` and `hostvocab` linters enforce, so one guard serves every
  inner harness.
- A job row is a line in one JSON Lines store per repository. `job exec`,
  `describe job` and `job exit` read and write it the same way from any host.
- The tier table is static and declared
  ([ADR 0009](../../decisions/0009-cost-profiles-for-agent-sessions.md)); a
  person changes it.

## Good

The model proposes `go test ./...`; the outer harness refuses it and names the
command whose result the cache and the affected set record:

```sh
magus run test .
```

The inner harness runs the command the model proposes next. Neither harness
asked the model whether the cache was honest.

## Bad

```text
orchestrator: read Claude Code's transcript, pick the tool Codex calls next,
and paste Codex's reply back into Claude Code
```

The orchestrator is a third agent loop driving two others. It depends on each
host's transcript format and tool names, it decides what runs next, which is
the inner harness's job, and a person reviewing the result cannot tell which
loop made which call.

## See also

- [A harness is not an agent thing](a-harness-is-not-an-agent-thing.md)
- [The harness belongs in the task runner](the-harness-sits-on-the-tool-layer.md)
- [Determinism at chosen points](../judgment/determinism-at-chosen-points.md)
- [Glossary: harness](../../glossary/harness.md)
