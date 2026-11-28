---
title: magus is its own first consumer
order: 45
description: magus's own repository gets no privilege a consumer couldn't have; it fills the same declarations any workspace does, and the binary never asks whether it is running in magus's own checkout.
tags: [design, principle, scope, layering]
---

# magus is its own first consumer

magus is built with magus, and that is useful for exactly one reason: this
repository meets the tool the way every other workspace does. The moment the
binary treats its own checkout as special, that stops being true. A fix that
works here proves nothing for anyone else, and the binary every user installs
starts carrying this repository's paths, targets and habits.

Three layers each own one kind of knowledge:

- **The product** is the binary, the shipped skills and the built-in spells.
  Everything in it must hold in any repository that uses magus, including one
  with no Go at all. It measures, records, compares and refuses on what a
  workspace declares.
- **This repository's workspace** is `hack/`, the root magusfile and
  `magus.yaml`. It is where magus decides how magus itself is developed: which
  calls this repository refuses, how its checkouts are readied, how its own
  binary is rebuilt.
- **Spells** hold the knowledge of one language or toolchain.

magus's own repository gets no privilege a consumer couldn't have. When
developing magus needs something the product lacks, there are two places it
can go. It becomes a general extension point that any workspace may fill, and
this repository fills it through a declaration like any other. Or it stays in
`hack/` as this repository's policy. It never becomes a branch in Go that
asks whether the tree is magus.

Before a line goes into Go under `internal/`, `cmd/`, `libs/` or `std/`, ask:

1. Would this line be true in a repository that is not magus and holds no Go?
   If not, it is not product code.
2. Does it name a path, target, tool or environment variable of this
   repository (`hack/`, `go-build`, `./cmd/magus`, `GOEXPERIMENT`, a script's
   flags)? Then it is this repository's policy. It goes in `hack/` or behind a
   declaration that Go reads.
3. Is it the knowledge of one language or toolchain? Then it is a spell.

The same questions apply to a verb or a flag. One that only this repository's
own sessions run is a cost every user pays in the help text, and it needs the
same case as any other capability.

## Enforced by

- `hack/lint/core-names-nothing-of-this-repository.buzz` counts the product Go
  lines that name this repository's paths, targets, tools or script flags. Each
  file is held to its count in the root `analyzers.yaml` baseline, which may only
  fall, so no new line lands and each one removed stays removed. It counts names,
  so a branch on where magus runs that names nothing, like `ownSourceRoot`, is
  still caught only in review.
- `ConsumerWorkspaceSuite` in `internal/guard/stale_binary_test.go` runs the
  stale-binary remedies in a workspace that is not magus and fails if one names
  `./cmd/magus`, `go-build`, `GOEXPERIMENT` or `hack/`.
- The `AGENTS.md` line that carries the first question.

## Good

A hook any workspace may declare, which this repository fills like any other:

```yaml
# magus.yaml
checkout:
  bootstrap: hack/dev/bootstrap-checkout.buzz
```

The binary reads the path and runs it. Nothing in Go knows what the script
does.

## Bad

```go
func ReadyCheckoutCommand(hook string) string {
    return shellLine([]string{"./magus", "buzz", hook, "--", "--here", "--verify-ready"})
}
```

The hook is a general extension point, but Go appends this repository's
script flags to whatever script a workspace declares. A workspace whose
`scripts/setup.buzz` knows neither flag is told to pass both. The arguments
belong in the declaration.

```go
if ownSourceRoot(root) {
    return "rebuild ./magus with `go run ./cmd/magus run go-build`"
}
```

The goal, never handing another workspace a line only magus's source can run,
is right. The mechanism makes the binary ask where it is running instead of
reading what the workspace declares.

## See also

- [Scope](../../scope.md#the-test)
- [Capability is not a reason](capability-is-not-a-reason.md)
- [Delete before you add](delete-before-you-add.md)
