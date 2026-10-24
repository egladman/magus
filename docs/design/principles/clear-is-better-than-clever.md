---
title: Clear is better than clever
order: 15
description: Write to the need in front of you, one way, the obvious way; clever earns its place only where a measurement says it must, and then it says why.
tags: [design, principle, code, simplicity]
---

# Clear is better than clever

Keep it simple, and write to the need in front of you. Build what the current
goal needs, ship it good enough, and iterate. Do not add a second code path, a
mode, an option or an abstraction for a need nobody has yet. Each one doubles
what a reader has to hold, and the need it guessed at usually arrives in a
shape nobody guessed.

Clever code scratches a real itch. It is satisfying to write and feels like
proof you are good at this. It is also, almost by definition, a departure from
convention, so the next person has to work out what you did and why before
they can safely touch it. Good code is code someone else can maintain. That
cost is not abstract: it is the bill you hand whoever works on this after you,
and more and more often that is an agent reading it cold. Buzz is the evidence
here: a language with close to one way to say a thing gets better agent output
than the most-trained languages on earth, and explicitness is the only variable
in sight.

This is not YAGNI taken whole. magus is aimed at the largest monorepos that
exist ([Built for the largest monorepos](built-for-the-largest-monorepos.md)),
and some of what that takes is engineering no sane person does for a small
repository. The rule is not "never optimize". It is: clear by default, and
clever only where a measurement says the clear version is too slow, with the
measurement and the trade-off written beside the code.

## Enforced by

- The `benchbeside` analyzer in [`libs/conventions`](https://github.com/egladman/magus/tree/main/libs/conventions) fails an `// optimization:`
  comment, or a `Cache` or `Memo` type, in a package whose tests hold no
  benchmark.
- The `// optimization:` comment carries the measurement and the trade-off, so
  the clever line names its price.
- Review against [Capability is not a reason](capability-is-not-a-reason.md): a
  second path needs a failure it removes.

## Good

Clever where it was measured, with the numbers and the cost beside it, from
[`cmd/magus/helpers.go`](https://github.com/egladman/magus/blob/main/cmd/magus/helpers.go):

```go
// optimization: run the first second of every process under loadGCPercent, not only the load.
//
//	measured: median wall, interleaved runs, n=60: commit-msg hook 74.1->61.6ms, tool-call hook
//	          89.7->78.1ms, describe targets 240.8->233.9ms; one fewer GC each.
//	trade-off: a long-lived process grows its heap 5x live for its first second.
```

## Bad

```go
// Load reads the config. legacy selects the old reader; lazy defers parsing until
// first use; both exist in case a future store needs them.
func Load(path string, legacy, lazy bool) (*Config, error)
```

Two flags for needs nobody has make four states, three of them untested. The
reader pays for all four on every call site, and the store they guessed at
will not look like the guess.

## See also

- [The Go proverbs, beyond Go](the-go-proverbs.md)
- [Delete before you add](delete-before-you-add.md)
- [Built for the largest monorepos](built-for-the-largest-monorepos.md)
