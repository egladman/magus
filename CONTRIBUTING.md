# Contributing to magus

magus is a one-person project. Issues and PRs are welcome; responses may be slow.
Open an issue before a large change so neither of us wastes the effort.

## Build and test

```sh
git clone https://github.com/egladman/magus
cd magus
go build ./cmd/magus
go test -race ./...
```

Integration tests sit behind `//go:build integration` and are named
`TestIntegration*`. `go test ./...` runs the fast unit tests; `go test
-tags=integration ./...` runs everything.

Lint and the vuln check live in a separate `go.tool.mod`, so the linter's large
dependency tree stays out of magus's library module graph:

```sh
go tool -modfile=go.tool.mod golangci-lint run
go tool -modfile=go.tool.mod govulncheck ./...
```

## Performance changes need evidence

This is the rule I care about most. Any change that claims to be faster ships
with a checked-in `Benchmark*` and the benchstat numbers behind it. No
speculative micro-opts.

Capture a baseline, make the change, then compare:

```sh
go test -run=^$ -bench=. -benchmem -benchtime=2s -count=10 ./PKG > before.txt
# ... your change ...
benchstat before.txt after.txt
```

Put the benchstat rows in the commit message, not the tree. Leave an inline
`optimization:` comment at the hot path, in the form used in
[`internal/cache/mtime.go`](https://github.com/egladman/magus/blob/main/internal/cache/mtime.go):

```go
// optimization: <what changed in one line>.
//   measured: <BenchmarkName> <delta> (benchstat, n=N).
//   trade-off: <legibility/portability cost>.
//   assumes: <platform/kernel/build constraint>.
```

so the trade-off is reviewable without re-running the bench. Per-OS fast paths
(see [`internal/cache/reflink/`](https://github.com/egladman/magus/tree/main/internal/cache/reflink/)) always keep a portable
fallback; never gate behavior on a fast path.

## Docs site

The docs site under `website/` is generated into the committed `website/gen/`
tree; regenerate and commit it after any doc change:

```sh
magus run generate:rw website   # re-render, keep the output
# review `git status website/gen`, then commit gen/ alongside your source edit
```

A plain `magus run generate website` gates on drift and fails if `gen/` was not
re-rendered, so CI catches a forgotten regen.

Pages use extensionless URLs (`/magus/documentation/`, served from
`documentation/index.html`). If you rename or move a page, keep the old URL alive
by listing it under `aliases:` in the page's frontmatter, so external links do
not die:

```yaml
---
title: Download
aliases: [install] # clean, gen-root-relative old paths
---
```

The build emits a redirect stub at each alias and fails if an alias collides with
a real page or is claimed twice.
