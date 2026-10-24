---
title: A harness is not an agent thing
order: 27
description: The checks, formatters, linters, compiler errors and gates around a change are one harness, and they hold a person to the green path the same way they hold an agent.
tags: [design, principle, harness, agents]
---

# A harness is not an agent thing

The word harness spread with agents, and most people now read it as agent
machinery: hooks, permission prompts, tool wrappers. I think that reading is
too literal, and it misses the point. A harness is the deterministic part of
the work around a change, whoever is making it. A compiler error, a formatter,
a linter, a spell checker, a type check, a test suite and the gate a branch has
to pass before it merges were a harness long before any model ran in a
terminal. They guide a person to the green path and refuse the change that
leaves it.

Seen that way, there is no separate agent harness to design. The guard that
denies an agent's raw `go test` and hands back `magus run test` is the same
idea as the lint that fails a person's push: one set of checks, built once,
held to the same evidence, reaching whoever is at the keyboard. An agent needs
the harness more often and more strictly, because advice that only prints is
routed around ([ADR 0013](../../decisions/0013-the-guard-denies.md)), but the
harness itself is not about agents. That is why magus builds it as developer
tooling first, and why nearly every rule the guard enforces is a rule a person
would want too.

## Enforced by

- The guard's verdicts come from the same declared outputs, leases and gates
  that `magus affected ci` runs for a person.
- [Human-first is the AI integration](human-first-is-the-ai-integration.md): no
  check exists for an agent alone, with the job store as the named exception.
- The [glossary entry for harness](../../glossary/harness.md) defines it by what
  is deterministic, not by who drives it.

## Good

One gate, whoever runs it:

```sh
magus affected ci
```

## Bad

```text
.agent/rules.md:  Always run the linter before committing.
.github/workflows/ci.yml:  (no lint step)
```

The rule exists only as advice to a model. A person is never held to it, an
agent reads it and skips it, and nothing checks either of them.

## See also

- [Determinism at chosen points](../judgment/determinism-at-chosen-points.md)
- [Friction that carries meaning](friction-that-carries-meaning.md)
- [Glossary: harness](../../glossary/harness.md)
