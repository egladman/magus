---
title: Abstract at the right layer, or not at all
order: 25
description: A package, module or layer earns its boundary by what it hides; a thin one that renames a function makes every reader open five files to follow one call.
tags: [design, principle, abstraction, packages]
---

# Abstract at the right layer, or not at all

Over-abstraction is the failure I watch for most, in my own code and in what an
agent hands me. Engineers are trained to abstract, and the instinct is usually
right, which is why it goes wrong so quietly. A layer built on top of another
team's abstraction, under a third team's, is three models of a thing nobody at
the top can see. Each layer looks reasonable to whoever wrote it, and working
on the top one means holding all three in your head.

It happens inside one repository too, and TypeScript is where I see it most: a
directory of one-function modules, a barrel file re-exporting them, and an
import chain five files long before you reach the line that does anything. To
learn what one call does, you open every file in its lineage. Nothing was
hidden by any of those boundaries; they only moved the code further from the
reader.

A boundary earns its place by what it hides. A package with one exported
function over four unexported helpers is real encapsulation at any size. A
package that is one file and its test, exporting one function nothing else
needed separated, is a rename with an import path. So is a Go interface with
one implementation and no second on the way. The deliberate exception is a
package stubbed for work that is planned and named, and it says so.

You cannot abstract a system you do not understand, and you cannot redesign one
either. Before proposing a boundary, a rewrite or a new architecture, learn
what drove the current design, where it hurts today and what it is for. A
fence whose purpose you never learned looks like an obstacle, and taking it
down is how the problem it held back comes back worse. Engineering for its own
sake, a redesign that serves the person proposing it more than the people using
the thing, is not worth the time it costs. In this repository the record is
there to read: the [decisions](../../decisions/0001-bounded-concurrency-with-recursive-invocation.md),
[A record of refusals](../refusals.md), and `magus explain` on the code itself.

None of this is against structure. A directory tree that mirrors the domain,
nested where the domain nests, with each package deep enough to justify its
name, is one of the best things a codebase can have: you can read the
architecture off `ls`. Done halfway, with packages too thin to hold anything,
the same nesting becomes a maze. Hierarchy is the goal; thin layers are its
counterfeit.

The same discipline reaches the words. magus names one concept once (target,
spell, charm, op) and reuses it everywhere, rather than wrapping the same idea
in a new term at each layer.

## Enforced by

- The `file-split` conformance metric keeps a concept in one file until that
  file passes 1500 lines, so a package does not fan out into thin files early.
- The `export-scope` and `dep-fanout` conformance metrics, ratcheted in
  [`hack/policy/conformance.jsonl`](https://github.com/egladman/magus/blob/main/hack/policy/conformance.jsonl), flag an export nothing outside its package
  uses and a package that imports too much.
- The [architecture review skill](https://github.com/egladman/magus/tree/main/internal/agent/skills/magus-architecture-review)
  names the too-thin shapes to check before proposing a boundary: a package
  imported only from inside its own subtree, a package with exactly one
  importer, and a single file with a single exported symbol.
- Nothing yet fails a one-file package outright; that metric is a debt in
  [Where this is strained](../strained.md).

## Good

One package, deep enough to hold its concept, its files named for the parts of
it ([`internal/guard`](https://github.com/egladman/magus/tree/main/internal/guard)):

```text
internal/guard/
  catalog.go   every rule, its name and what it catches
  shell.go     judging a shell command
  push.go      the push rules
  write.go     judging a file write
  lease.go     who owns a path right now
```

## Bad

```text
src/utils/index.ts           export * from "./format";
src/utils/format/index.ts    export * from "./formatDate";
src/utils/format/formatDate.ts
                             export const formatDate = (d: Date) => toIso(d);
src/utils/format/toIso.ts    export const toIso = (d: Date) => d.toISOString();
```

Four files and two barrels to reach `toISOString()`. Every boundary is a rename,
none of them hides anything, and a reader or an agent opens all four to learn
that.

## See also

- [Sit next to the tools, never in front of them](sit-next-to-the-tools.md)
- [A copy can be the right answer](a-copy-can-be-the-right-answer.md)
- [The Go proverbs, beyond Go](the-go-proverbs.md)
- [Clear is better than clever](clear-is-better-than-clever.md)
