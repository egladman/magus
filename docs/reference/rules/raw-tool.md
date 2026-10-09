---
title: "raw-tool: a toolchain command a spell already wraps, run outside the cache"
description: "An advisory by default: it explains, and blocks nothing, on a toolchain command a spell already wraps, run outside the cache."
tags: [guard, rules, raw-tool, advise]
---

# raw-tool

An advisory by default: it explains, and blocks nothing, on a toolchain command a spell already wraps, run outside the cache.

## What it catches

A toolchain command a spell already wraps, run outside the cache.

## Why

magus covers these exactly and adds cache, sandbox and affected tracking, so the refusal costs nothing: `magus run <target> <project>`, and `magus describe targets -o name` lists what this workspace calls them. Tool flags go after `--`. A raw WRITE (codegen, a formatter with -w/--write/--fix, `go mod tidy`, build output landing on a tracked path) is the firm half: it leaves the owning target reporting drift it did not cause, and that has no exceptions. The guard reads the command being RUN, so a wrapper, a `VAR=value` prefix or `bash -c` reaches the same verdict, and `go -C <dir> <verb>` reads the same as `go <verb> -C <dir>`. Asking a tool for its usage or version runs nothing over the tree and passes: `go clean --help`, `gofmt -h`, `go help clean`, `govulncheck -V`. The help flag has to be the tool's own, last on the line, after a subcommand a spell renders; one handed to a program is work, so `go run main.go --help` and `go test ./... -args --help` are refused. `gofmt -l` and `gofmt -d` pass too, without `-w`: they list or diff and write nothing, so they leave no drift for the format target to report. `go list` passes because no spell renders it. One command is exempt, in a checkout of magus itself: `GOEXPERIMENT=jsonv2 go run -trimpath ./cmd/magus run go-build --no-cache .`, alone on its line with no wrapper and no other prefix, in a checkout root that has no `magus` binary yet, is advised rather than refused, because a fresh checkout has no other way to get its first binary. It runs the real go-build target (its generate steps and stamped link) instead of a bare link, so the first binary is the one every later build would make. `--no-cache` because that target's key cannot express the embedded-spell ordering and once replayed a binary missing tools; Go's content-addressed build cache stays on, `-trimpath` matches the target's own build so packages compile once, and `GOEXPERIMENT=jsonv2` because magus refuses to compile without it and a fresh checkout cannot count on mise to set it. Every other raw go command in that root, a bare `go build -o magus ./cmd/magus` included, is refused and served the bootstrap. Once the binary exists the deny applies again and names `./magus run go-build .`. None of this is a leased worker's: there is one binary per base, the orchestrator builds it in the root and places a copy in each worker checkout, so a worker is refused every build of it and told to ask the orchestrator to run `magus buzz hack/dev/bootstrap-worktree.buzz -- --job <id>`. A checkout that cannot load its own sources (MGS1021) has a second exemption, because no target can run there and the bootstrap fails to compile against generated files that lag their sources: alone on its line, with no environment prefix but `GOEXPERIMENT`, the relink MGS1021 prints (`go build [-trimpath] -o magus ./cmd/magus`) and the generators the `*_generate` targets run (`go generate <package>` inside the checkout, `go run [-trimpath] ./cmd/magus-utils <generator>`, never its release subcommands) are advised rather than refused, with a binary or without. The guard learns this by loading the workspace, and only for one of those lines; the binary judging is the checkout's own `./magus` when there is one. Once the workspace loads, they are refused again. A run outside the target also leaves the Go build cache holding uninstrumented results.

## Default and override

By default this rule takes the decision `advise`. A workspace sets it by name, in its root
magusfile, to `deny`, `advise` or `off`:

```buzz
magus\guard.builtins({"raw-tool": "deny"})
```

A loosening takes effect once it is committed; a tightening applies at once.

## Seeing it

A verdict names its rule in brackets, which is how you got here:

```text
advise [raw-tool]: ...
```

`magus describe rule raw-tool` prints the same entry at a terminal, and
`magus describe rules` lists every rule this workspace enforces.

## See also

- [All rules](index.md) - what this workspace enforces, deny first
- [The guard](../../guides/integrations/agents/guard.md) - how a verdict is reached and wired
- [Setting a built-in rule](../../guides/integrations/agents/guard.md#setting-a-built-in-rule) - how a workspace changes this default
