# Contributing to magus

Thanks for being here. magus is a one-person project, and anyone who takes the time
to try it, file an issue, or send a patch is doing me a favor.

Issues and PRs are both welcome. Replies may be slow, which is about how much time
I have rather than how interested I am. If you're planning something big, open an
issue first so we can talk it through before you spend an evening on it.

## Using AI tools

I use AI assistance to build magus, so it would be strange to tell you not to. Use
whatever you like.

If you're new to the codebase, one suggestion rather than a rule: make your first
couple of PRs without it, and keep them small. A docs fix, a test, a one-line
change. Nothing controversial.

That's less about the tools than about the size of the change. What I'd rather not
get is a big multi-file PR rewriting how something works, from someone who hasn't
had a chance to poke around yet. These tools are about as good as the context of
the person steering them, and when they're wrong they're usually wrong in a way
that still reads fine, which is hard to spot if you're new to a codebase. Doing a
couple of small things by hand is the quickest way past that.

After that, go for it. I'm looking at the change, not at how you made it: can you
explain what it does and why, does it match the conventions below, does
`magus affected ci` pass. You don't need to tell me either way.

And if any of this is unclear, or the setup fights you, please open an issue. I'd
much rather answer a question than have someone give up on it quietly. Friction is
the thing this project exists to remove, so hitting some on the way in is worth
telling me about.

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

Lint and the vuln check run as pinned binaries from `mise.toml`, so the linter's
large dependency tree stays out of magus's module graph entirely:

```sh
magus run lint
```

They deliberately do not go through `go tool`. That compiles the tool from source
with whichever `go` leads `PATH`, which is how CI once built golangci-lint with a
different toolchain than the one it analyzed with, and panicked. `mise install`
gets you both binaries at the pinned versions.

## Adding to a public surface

magus promises that a magusfile which works today keeps working, and there is no
plan for a 2.0. [docs/concepts/compatibility.md](docs/concepts/compatibility.md)
is the promise itself; this is how to work inside it.

**Add, never substitute.** A new `magus\project` key goes beside the existing
ones, a new protobuf field takes a new number, a new flag joins the current set.
If you find yourself changing what an existing name means, that is the thing the
promise forbids, and the answer is a second name rather than a redefinition.

**Three gates catch a break before review does**, one per surface:

| Surface           | Gate                                                    |
| ----------------- | ------------------------------------------------------- |
| CLI               | `internal/manpage/testdata/api.lock`, a drift-gated snapshot of every subcommand, flag, config key, and target |
| protobuf          | `buf breaking`, composed into `lint`                     |
| magusfile keys    | the `required_version` floor, plus doctor's check that the floor is accurate |

A diff to `api.lock` is the signal to read carefully: a line removed there is a
removed public surface. Regenerate it with `go generate ./internal/manpage/...`
after an intentional addition, and treat a deletion as a question rather than a
regeneration.

**A new key means the workspace needs a newer magus.** Additive changes are safe
for existing users and unsafe for the repo's own magusfile, which CI runs against
a pinned release. The rule is release-first: if `magusfile.buzz` starts needing a
feature no release contains, cut the release before the magusfile depends on it.
When you hit this locally the symptom is a load error from the pinned binary,
which is the mechanism working rather than a problem to route around.

Raise `required_version` in `magus.yaml` **in the release commit, not in the
feature commit.** The floor names a released version, and between releases a build
describes itself from the last tag - a tree that is 42 commits past `v0.3.0`
reports `v0.3.0-42-gabc`, which compares as 0.3.0. A floor set to the upcoming
release therefore rejects a source build of the very commit that raised it, and
this repo builds magus from source in CI. Add the feature first, cut the tag, then
raise the floor to the tag.

Downstream users, who run released binaries, get
[MGS1021](docs/reference/codes/magusfile/MGS1021.md) naming both fixes instead of
an error from wherever the magusfile happened to break first.

**Before 1.0, renaming is on the table; after, it is not.** magus is pre-1.0, so a
badly chosen name can still be fixed, and the changelog records it under
**Breaking**. That window closes at 1.0. If you are adding a name you are not sure
about, say so in the pull request - it is much cheaper to argue about it now than
to keep it forever.

**Deprecating** means "there is a better way now", not "this stops working". Keep
the old surface working, point at the replacement where someone meets it rather
than only in the changelog, and list it under **Deprecated**.

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

The docs site under `docs/` renders into `docs/gen/`, which is **not** committed.
`.github/workflows/publish-site.yaml` renders it on every push to `main` and publishes
that. Render locally to check your change:

```sh
magus run generate:rw docs   # re-render into docs/gen/, then open it
```

It was committed for years. The reason it no longer is: every page embeds the
commit that last touched its source, so committing a source change immediately
staled the rendered output committed beside it. That bought a second "refresh
generated metadata" commit after every real one, and amending could not escape
it, because the new hash restales the footer it just recorded. Rendering at
deploy time knows the final commit already.

What still gates on drift is the generated Markdown that *is* tracked:
`MAGUS.md`, the root `CHANGELOG.md`, `docs/src/gen`, and the derived pages under
`docs/reference/`. A plain `magus run generate docs` fails if any of those was
left un-regenerated, so CI still catches a forgotten regen for everything a
reader can find in the repository.

Two things under `docs/` stay tracked on purpose:

- `docs/vendor/playground/` holds the playground wasm and its TinyGo glue. They
  are the only published bytes CI cannot reproduce, because it has no TinyGo.
  The render copies them into `gen/`. Rebuild with `magus run build-playground docs`.
- `docs/active.urls.lock` is the ledger proving a previously published URL never
  starts 404-ing. That gate only works if the ledger outlives a single build.

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

### Point your worktree's merge driver at your own build

The rule above is about CI. Locally there is one place where "the released
binary" is the wrong answer, and it is easy to lose an afternoon to:

```sh
./hack/install-dogfood.sh
```

`magus init` registers the git merge driver as whichever `magus` leads PATH.
That is correct for someone *using* magus, because the registration survives an
upgrade-in-place. In a magus worktree it is backwards: PATH holds a release, and
the merge driver is part of what you are changing. A rebase then resolves every
generated conflict with the release, not with your tree.

That is not a theoretical gap. A released driver regenerated the whole docs site
once per conflicted file, so a rebase over a handful of generated files looked
exactly like a hang, while the version in the tree resolved the same file in
under a second.

The script builds `magus-dev` and registers it with `git config --worktree`, so
the override stays local to that checkout. `.git/config` is shared by every
linked worktree, so an absolute path there would aim all of them at one
checkout's binary. It is a separate binary from `./magus` on purpose: `./magus`
is rebuilt constantly while iterating, and swapping the driver's binary during a
rebase changes the tool mid-operation.

Re-run it after changing the merge driver, or the registration keeps resolving
with the older build.

## Naming

Names are the API most people meet first, so they get decided deliberately
rather than by whatever the file was called. From the outside the results can
look arbitrary - `go` lives in `spells/golang/`, `ls` and `describe` both list
things - so here is the reasoning, which is not arbitrary.

### Source layout and registered identity are independent

A spell's directory and the name it registers answer different questions, and
they are allowed to differ.

The **directory** is where a contributor finds the code. The **registered name**
(`mgs_getName`) is the identity users type and every listing prints.

```text
spells/golang/          <- idiomatic directory name for Go source
  registers "go"        <- the language's actual name
```

Both are right. `golang` is the conventional directory spelling; `go` is what
the language is called. Forcing one to match the other would make one of them
wrong. Do not "fix" a mismatch on sight - check which question each is
answering first.

### A registered name has to stand alone

The registered name appears in `magus describe spells`, in diagnostics, and in
error text, always without the directory around it to supply context. So it must
be unambiguous on its own:

- **Name the thing, not the job it does here.** The S3 backend registers
  `aws-s3`, not `s3-cache`. Its siblings are named for products, and a
  capability name reads as a different kind of entity in the same list.
- **Qualify when the bare word names nothing.** `spells/github/actions/`
  registers `github-actions`, not `actions` - "GitHub Actions" is the product's
  real name, and `actions` alone identifies nothing.
- **Never take a word the core model already owns.** `spells/gitlab/ci/` used to
  register `ci`, which collides with the `ci` *target* that `magus affected ci`
  anchors on. A listing then showed a `ci` spell beside a `ci` target meaning
  entirely different things. It registers `gitlab-ci`.

### One verb, one job

Subcommands are split by the question they answer, not by the noun they touch,
so two verbs never differ only in verbosity:

- **`ls` enumerates.** Breadth. What exists here, what can I run. Everyday.
- **`describe` explains one thing fully.** Depth. A definition plus the complete
  record. Occasional.

That is why `magus ls` shows targets but not source globs: the globs are the
full record, which is `magus describe project`'s job, and printing them in both
would make the boundary mush. When adding output, ask which question it answers
and put it in exactly one place.

Prefer a **noun on an existing verb** over a new subcommand. `magus ls targets`
rather than `magus targets`, because the latter invites `magus spells`, then
`magus charms`, and the surface becomes a pile of noun-commands with no rule to
learn. One verb, a noun that says what.

Enumeration is spelled `ls` everywhere - `magus ls`, `magus run ls`,
`magus memory ls` - never `list`.

### Package names mirror the contract they serve

Where a package exists to serve one wire contract, it takes that contract's name,
so the two are correlated by reading rather than by grep. A wire-mapping
subpackage `internal/handler/<name>` owns the over-the-wire concerns of the
protobuf package `magus.<name>.v1`:

```text
proto/magus/graph/v1     <->  internal/handler/graph
proto/magus/status/v1    <->  internal/handler/status
proto/magus/viewer/v1    <->  internal/handler/viewer
```

Adding `proto/magus/foo/v1` means adding `internal/handler/foo` - same name, no
exceptions for the wire packages. Two subpackages there are deliberately not
proto-backed and so are not part of the mapping: `mcp` is a protocol adapter and
`trailrpc` is a transport concern.

[`internal/handler/README.md`](https://github.com/egladman/magus/blob/main/internal/handler/README.md) is the authority and
carries the full table plus what does and does not belong in the layer; keep the
rule there rather than restating it per package.

### Hints belong in `clihint`

A command path printed inside output goes through
`internal/interactive/clihint`, never a string literal. A drift test walks every
registered command and asserts it still resolves, so a rename cannot leave a
hint pointing at a command that no longer exists. That has already happened once:
a failing target printed `magus query <ref>` long after the command became
`magus query output <ref>`.

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
