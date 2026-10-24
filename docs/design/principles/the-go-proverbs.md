---
title: The Go proverbs, beyond Go
order: 17
description: Rob Pike's Go proverbs hold in every language here; where one has an enforcement point the page names it, and where it has none that is a debt.
tags: [design, principle, code, go]
---

# The Go proverbs, beyond Go

magus is written in Go, and the [Go proverbs](https://go-proverbs.github.io/)
are the shortest statement I know of how I want code written. Most of them are
not about Go. They hold for the Buzz in a magusfile, the TypeScript in the
console, and the shell in a hook template, so this page reads each one against
the whole tree.

- **Clear is better than clever.** The rule under every other one; it has
  [its own page](clear-is-better-than-clever.md).
- **The bigger the interface, the weaker the abstraction.** A swappable part is
  a small interface built only where a second implementation exists
  (`WithVCSDriver`, `WithCacheBackend`). The same holds for a spell's op, a CLI
  verb and an MCP tool: a wide one promises less about each call.
- **Make the zero value useful.** A caller that passes no option to
  `magus.Open` gets the ordinary workspace, and each option changes one thing.
  The default is still said out loud where it matters
  ([No silent defaults](../judgment/no-silent-defaults.md)).
- **Errors are values. Don't just check errors, handle them gracefully.** An
  error carries what happened and what to do next. In TypeScript the console
  returns errors as values too, and its only `throw` is `unreachable(x: never)`.
- **Don't panic.** A panic is for a broken invariant, never for input a caller
  could have sent.
- **Reflection is never clear.** A type Buzz needs is mirrored by a generator at
  build time, never reflected at run time, so the cost and the shape are both
  visible in the tree.
- **Cgo is not Go; syscall must always be guarded with build tags.** A platform
  fast path lives in its own file behind a build tag, beside the portable
  version it must agree with.
- **With the unsafe package there are no guarantees.** Reached for only with a
  benchmark and an `// optimization:` comment beside it.
- **Gofmt's style is no one's favorite, yet gofmt is everyone's favorite.**
  Every language here has one formatter and nobody relitigates it: gofmt, dprint,
  Biome.
- **Don't communicate by sharing memory; share memory by communicating.
  Concurrency is not parallelism.** Jobs share a file by a declared claim, not by
  racing on it ([ADR 0010](../../decisions/0010-jobs-share-a-file-by-declaration-claims.md)).
- **Documentation is for users.** A doc comment states the contract a caller
  needs without the source; the comment budget inside a function body is close
  to zero.
- **A little copying is better than a little dependency.** It has
  [its own page](a-copy-can-be-the-right-answer.md).

## Enforced by

- `gofmt`, dprint and Biome run as format targets, and the drift gate fails an
  unformatted file.
- The `benchbeside` analyzer holds `// optimization:` comments to a benchmark.
- `gochecknoinits` fails a new `init()`.
- The rest live in review today. A linter for the interface size
  (`interfacebloat`), and an import rule that keeps `reflect` and `unsafe` to an
  allowlist, are candidates listed in [Where this is strained](../strained.md).

## Good

The SDK swaps a part through one small interface and one option:

```go
ws, err := magus.Open(ctx, root, magus.WithVCSDriver(driver))
```

## Bad

```go
type Store interface {
    Get(key string) ([]byte, error)
    Put(key string, value []byte) error
    List(prefix string) ([]string, error)
    Watch(prefix string) (<-chan Event, error)
    Compact() error
    Stats() Stats
    Close() error
}
```

Seven methods for a caller that needs one. Every new implementation has to fake
six it never uses, and the interface no longer says what any single caller
depends on.

## See also

- [Clear is better than clever](clear-is-better-than-clever.md)
- [A copy can be the right answer](a-copy-can-be-the-right-answer.md)
- [Go proverbs](https://go-proverbs.github.io/)
