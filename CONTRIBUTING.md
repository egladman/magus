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

## Dogfooding: how magus builds magus

The CI never uses HEAD to build the workspace. The binary that compiles
this repo is the latest released version; any feature the `magusfile.buzz`
uses must ship in a release first. This is the only stance that keeps the
dogfooding contract deterministic - a CI run that compiled magus with a
newer magus would be testing its own next release, not the one users have.

In practice the workflow uses the `setup-magus` action with
`installation-strategy: source` and `git-ref: ${{ github.sha }}` for the
*rare* case where no prebuilt artifact exists for the SHA (right after a
push to main before a release). For the common case, the prebuilt artifact
from the previous release is the binary every step uses. The source-build
fallback carries a supply-chain gate - the commit must be reachable from
`main` - because the build is unverified otherwise.

If your change adds a flag, a target, or a host-binding shape the
magusfile uses, that change ships in a release *before* the magusfile
change merges. The other way around is a breaking-change signal, not
something to paper over.

## Workflow targets, not inline shell

The GitHub Actions workflow files are intentionally thin. Every meaningful
CI operation lives in a target in `magusfile.buzz`, not in a workflow YAML
step. The workflow just orchestrates: it sets up the toolchain, calls the
target, and uploads artifacts. Host-specific knowledge (GitHub Actions
concepts like `$GITHUB_STEP_SUMMARY`, the `gha` charm, the
`MAGUS_INSIGHT_OUTPUT_PATH` env var) lives at one boundary, and every other
piece of the pipeline stays portable.

Concretely:

- **The workflow YAML** names jobs and steps, sets `MAGUS_*` env vars that
  host the host-specific plumbing, and uploads artifacts. It never
  contains logic that could live in a target.
- **The magusfile targets** hold the actual work: which projects to fan out
  over, what to render, how to interpret drift, what to write to a sink
  the workflow supplies via env.
- **A target that wants host-specific behavior** declares it via a charm
  (`gha`, `cd`, `rw`) and reads env vars whose names are generic
  (`MAGUS_INSIGHT_OUTPUT_PATH`, not `GITHUB_STEP_SUMMARY`). The workflow
  sets the env to whatever the host provides.

This way a future port to a different CI system only needs a thin
workflow file plus the same magusfile. The targets, charms, and env-var
naming are portable.

## Workspace references: `workspace://path` and friends

Project paths accept four equivalent forms; only one canonical form survives
on the wire:

```sh
.                     # bare path, shell convention
pkg/foo               # bare nested path
workspace://.         # explicit root, scheme form
workspace://pkg/foo   # explicit nested path, scheme form
```

The CLI accepts all four at every project-arg surface. The canonical form
that appears in error messages (where a user can copy-paste it back as a
command) is `workspace://pkg/foo` - rendered via `types.WorkspaceRef`. The
form that appears in logs, Mermaid labels, and table cells is the bare
path (`pkg/foo`) - rendered via `types.ProjectLabel`. The browser shows
`example.com` until you hover; magus shows `pkg/foo` until you pipe it.

Both forms route through `types.ProjectRef{Path, Dir}` so the two helpers
share one definition. New rendering rules go on the struct's methods, and
both helpers pick them up automatically.

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
