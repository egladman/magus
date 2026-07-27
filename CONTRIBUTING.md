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

The docs site under `docs/` is generated into the committed `docs/gen/`
tree; regenerate and commit it after any doc change:

```sh
magus run generate:rw docs   # re-render, keep the output
# review `git status docs/gen`, then commit gen/ alongside your source edit
```

A plain `magus run generate docs` gates on drift and fails if `gen/` was not
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

## Release-signing key rotation

The release public key has a few intentional, reviewable copies: the binary's
`internal/selfupdate/release.pub`, `docs/gen/install`, the setup action, and
the download guide. `TestReleaseTrustAnchorMatchesInstallerAndCI` makes a
mismatch between those copies fail CI. Never put the private Ed25519 seed in
the repository or a release artifact.

For a planned rotation, create a compatibility release signed with the current
key but built with the replacement public key. Users who install that release
can verify later releases signed by the replacement key. Then move the
`MAGUS_SIGNING_KEY` secret to the replacement private key and publish future
releases with it. Update every public-key copy above in the same change, run
the trust-anchor test, and dry-run the installer before publishing.

A compromised key cannot be revoked from binaries that already trust it: those
binaries cannot distinguish a legitimate replacement from an attacker-signed
one. Treat that as an incident: stop using the compromised signing secret,
publish the replacement binary and public key through an independent trusted
channel, and ask affected users to reinstall manually. Do not describe that
path as an automatic update.
