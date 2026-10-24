---
title: The harness belongs in the task runner
order: 31
description: Loosen the reins at the right layers; magus builds the harness on the task runner's record of declared inputs and outputs, and the CLI, the harness and the Go SDK all read that one record.
tags: [design, principle, harness, agents, sdk]
---

# The harness belongs in the task runner

I think most agent frameworks write the harness at the wrong layer. They're too
tight, they ship in lockstep with new models, and they're always catching up,
because a frontier model is a moving target. The answer is to loosen the reins,
but at the right layers: intentional determinism placed strategically, and
the model left to do its job. A framework that also wires up the LLM providers
owns a seam the developer implementing it should own.

The right layer is boring. It's a task runner, a modern repo tool. A build tool
already has to know what every target reads and writes, or it breaks builds.
magus keys its cache on those declared inputs and outputs, by content, and its
graph knows what a diff reaches. The harness is built on that record, so it
gets caching, affected sets and reproducible runs from the layer that owns
them, and re-derives none of them.

<!--diagram:harness-layer-->

It is one Go module. The root package is the Go SDK, and everything uses it:
you import it, or you go through the `magus` binary, which is both the CLI and
the harness. A person and an agent get the same answer because they ask the
same code. magus never calls a model; the loop belongs to the agent host
([The outer harness stays outside](../judgment/the-outer-harness-stays-outside.md)).

A harness here is a guiding hand: it reinforces, it enforces, and it can block.
What a human trips on, an agent trips on, so the same actionable error that
shows a person the good and the bad path is what the agent reads. Some
nondeterminism stays, on purpose; a harness that tries to pin every step is
fighting its own model.

## Enforced by

- [A record of refusals](../refusals.md) lists an agent loop in the Go SDK and a
  model adapter as never built.
- The guard refuses a hand edit to a declared output and a write to a path
  another job has leased, reading the same record `magus affected ci` reads for
  a person ([A harness is not an agent thing](a-harness-is-not-an-agent-thing.md)).
- `cmd/magus` imports the root package `github.com/egladman/magus`, the package
  a Go program imports ([One source, every door](one-source-every-door.md)).

## Good

The plan a CI matrix uses to shard affected projects is the plan a person uses
to split work across agents. Nothing about it was built for agents:

```sh
magus affected ci --plan
```

## Bad

```ts
const runTests = createTool({
  id: "run-tests",
  inputSchema: z.object({ pkg: z.string() }),
  execute: async ({ pkg }) => cache.getOrRun(`tests:${pkg}`, () => sh(`go test ${pkg}`)),
});
```

The cache key is the argument, not what the tests read. Edit a file under `pkg`
and the tool hands back yesterday's pass, and nothing tells the agent or the
person who reruns `go test` by hand and gets a different answer.

## See also

- [Abstract at the right layer, or not at all](abstract-at-the-right-layer.md)
- [Human-first is the AI integration](human-first-is-the-ai-integration.md)
- [Sit next to the tools, never in front of them](sit-next-to-the-tools.md)
- [Built for the expensive era](built-for-the-expensive-era.md)
- [The Go SDK](../../reference/go-sdk.md)
