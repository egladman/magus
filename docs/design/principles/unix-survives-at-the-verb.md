---
title: Unix survives at the verb, not the binary
order: 1
description: Each verb answers one question, deterministically, from declared sources, and stops; the binary is as big as the monorepo problem.
tags: [design, principle, cli, unix]
---

# Unix survives at the verb, not the binary

One tool that does one thing extremely well does not survive contact with a
monorepo, and magus does not claim the lineage: a monorepo build tool has to
understand several languages, every project in the tree, caching, scheduling,
and what a change reaches. That is already many things. What survives the
translation is the discipline, relocated to the verb. Each verb answers one
question, deterministically, from declared sources, and stops. Output reads at
a terminal and parses in a pipe (`-o name`, `-o json`, `-o template=`; the
pipe is for people, and the agent guard routes an agent to `-o` instead,
because a pipe replaces the exit status and a failing gate then reads as 0). A
non-zero exit means what it has meant for fifty years. Everything magus draws degrades
to plain text when there is no terminal to draw on, and the pinned band never
takes the screen, the alternate buffer, or your scrollback. Composition
happens where a monorepo needs it (in the graph, through `ctx.needs`), not by
pretending the binary can be smaller than the problem.

The scope test governs what a verb may be, and the one-vocabulary rule
(target, spell, charm, op, each named once and reused everywhere) keeps the
command set predictable. That prevents the failure in both directions: claiming a
minimalism the tool cannot carry, and the house dialect with no rule you can
hold in your head, the one that makes users look things up forever and then
teaches their agents the same confusion.

The discipline reaches the code too:

- **A main package only wires.** A command parses its flags, builds its
  dependencies, calls a package and prints the result. Logic that computes
  belongs in the package that owns its data, where a test and a second caller
  can reach it.
- **One file until it is long.** A concept stays in one file until that file
  passes 1500 lines; only then does `R_<topic>.go` earn a place beside `R.go`.
  A platform build, generated output and a test are their own files at any
  size.

## Enforced by

- The [scope test](../../scope.md#the-test), applied in review to every new verb.
- The `main-logic` conformance metric fails a main-package function that
  computes without calling another workspace package; the ratchet
  ([`hack/policy/conformance.jsonl`](https://github.com/egladman/magus/blob/main/hack/policy/conformance.jsonl)) lists the ones that predate it.
- The `file-split` conformance metric fails a concept sibling that would fit in
  its root at 1500 lines or fewer, and moves with the `file-lines` ceiling.
- The agent guard denies piping magus output and names the `-o` form instead.

## Good

Ask the verb for the shape you want; the exit status stays the verb's.

```sh
magus affected ci --plan -o json
magus ls -o name
```

## Bad

```sh
magus run test . | tail -n 20
```

`tail` exits 0 whatever the tests did, so a failing gate reads as a pass in CI
and to an agent.

## See also

- [Scope](../../scope.md)
- [The knobs](../../scope.md#the-knobs)
- [Human-first is the AI integration](human-first-is-the-ai-integration.md)
