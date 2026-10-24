---
title: Inform people, contain the engine
order: 22
description: Guardrails live in the engine (the sandbox and the cache), not in a schema that predicts you; magus is strict about what it tells a person and permissive about what it lets them do.
tags: [design, principle, configuration, diagnostics]
---

# Inform people, contain the engine

CI is the weakest link in most chains I have worked in, and the usual response
is to wall it off: describe everything in hermetic JSON or YAML, permit nothing
outside the schema, and expect it to hold. That is a losing bet, because it
asks the tool's author to be a fortune teller. Every time they guess wrong
about how people use the thing, the tool's answer is that they cannot do that
here. It also gives the developer no grace: some days you push the half fix you
know is a half fix, because the constraint is a delivery date rather than an
engineering one.

These are developer tools, and the people running them are capable adults who
do this for a living. Security worked this out in 1975: Saltzer and Schroeder
listed psychological acceptability among their design principles, because a
protection mechanism that makes the work harder than going without it gets
routed around, and the route people find is worse than what you were guarding
against.[^saltzer]

So magus puts its guardrails in the engine. The language you write targets in is
a real language, with an escape hatch, because sooner or later you need one.
What contains it is a sandbox that declares what a target may touch and a
content-addressed cache that makes a run reproducible from its declared inputs.
The constraint sits where it can be enforced without predicting anyone.

The other half is where magus is opinionated. A build tool sees what you
cannot: the whole graph, every declared input and output, what a change
reaches. Staying quiet about a pattern that costs you a cache hit is not
neutrality, it is withholding. So magus is strict about what it tells a person
and permissive about what it lets them do: a diagnostic explains what tripped,
what it costs and what to do instead, and the person decides. The newcomer
follows it; the veteran reads the same thing and does something else.

Agents are the exception, and the reason is measured. Advice that only prints is
routed around by agents, so the guard denies an agent's action and hands back
the command to run instead ([ADR 0013](../../decisions/0013-the-guard-denies.md)).

## Enforced by

- The sandbox declares what a target may read and write, and a target past its
  ceiling fails with [MGS3011](../../reference/codes/sandbox/MGS3011.md).
- The cache keys on declared inputs, so a run is reproducible without a schema
  forbidding anything.
- Every diagnostic code has a page under [the diagnostics reference](../../reference/diagnostics.md),
  enforced by the `diagnostic-codes-have-docs-pages` lint.

## Good

A real language, contained by what it declares, from [`docs/magusfile.buzz`](https://github.com/egladman/magus/blob/main/docs/magusfile.buzz):

```buzz
export fun index_generate(ctx: magus\Context, args: [str]) > void !> any {
    ctx.writesFiles("MAGUS.md");
    ctx.readsFiles("magusfile.buzz", "../magus.yaml", "../internal/render/target_graph.go",
                   "../internal/render/md/md.go", "../internal/hint/cli_command.go");
    index\render("docs");
}
```

## Bad

```yaml
steps:
  - uses: org/approved-build@v3
    with:
      mode: release   # one of: debug, release
```

The schema decided in advance what a build may be. The day a project needs a
third mode, the answer is a ticket to whoever owns the action, and in the
meantime someone copies the action and edits it.

## See also

- [Behavior is code, facts are data](behavior-is-code-facts-are-data.md)
- [A refusal carries its reason](../judgment/a-refusal-carries-its-reason.md)
- [Friction that carries meaning](friction-that-carries-meaning.md)

[^saltzer]: Jerome H. Saltzer and Michael D. Schroeder, "The Protection of
    Information in Computer Systems", Proceedings of the IEEE 63(9), 1975.
