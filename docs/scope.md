---
title: Scope
description: The claim magus is built on - that a correct incremental build already requires a precise model of your repo, and everything else is a read of it. Includes the test for what belongs, the boundaries, and where the claim is strained.
tags:
  [scope, boundaries, design, philosophy, prior art, nx, dagger, bazel]
---

# Scope

A build tool that skips work has to know exactly what a change reaches. One that
replays a cached result has to know exactly what went into it. Both demand a
precise model of your repository: every project, every target, what each target
reads and writes, which tools it drives, at what versions.

magus builds that model because correctness leaves it no choice. Everything else
it does reads from it.

## One model, many reads

The model has to exist before magus can be trusted with a single skipped build,
so the expensive part is already paid for. Answering questions from it costs
almost nothing:

| verb                       | the question it answers                              |
| -------------------------- | ---------------------------------------------------- |
| `affected`                 | what does this diff reach                            |
| `query`, `explain`, `path` | what exists, and how do these relate                 |
| `describe file`            | is this generated, and by what                       |
| `refs`                     | where is this symbol defined and used                |
| the version window         | is the binary that ran inside the range you declared |

Take the version window, the newest of them. A tool's version already feeds the
cache key, so magus probes it on every build whether or not you declare a range.
Comparing that probe against a declared window took one comparison and about four
hundred lines, mostly tests and docs.

That is the shape of every verb above. None of them taught magus anything it was
not already forced to learn.

## The verb count

The design produces a long list, and the list is the first thing you notice.
magus runs your builds. It also caches them, tracks which projects a change
reaches, keeps a queryable graph of the repo, ships a daemon, serves a browser
console, exposes an MCP server, resolves secrets, sandboxes subprocesses, gates
dependency advisories, and checks the version of the toolchain a build ran on.

Read as a list, that describes a tool with no clear idea what it is. You have
watched build tooling accrete before: each addition defends itself, nobody
removes anything, and a few years on the command you type all day has a plugin
system, a release manager, and views about your changelog.

Building the wrong thing got cheap, too. You can generate coherent,
well-structured, completely misconceived code in minutes, so a design reading
well proves less than it used to. Someone still has to say no to work that would
otherwise ship.

The number of verbs matters less than where each one came from.

## The test

> Does this answer a question from what magus already knows, or does it require
> magus to learn something new about the world?

Reads stay small. Acquisitions are where a tool loses its shape, and you rarely
notice one arriving.

You can apply the test without trusting us. Ask what new thing the tool had to
learn, then read the diff.

Take the feature that prompted this page. The request was straightforward, fail
CI when the toolchain falls behind, and four designs died before one worked.

<details>
<summary>What got rejected, and why</summary>

A release-feed type in the binary, giving every tool a declared upstream feed so
magus could see which versions exist. Once magus has a releases concept it has to
answer what counts as a release, what LTS means, and when support ends. Three
opinions nobody asked it for.

A support-window table compiled in and refreshed by CI. End-of-life data is not
guaranteed to exist: around five of the eighteen tools in this repo publish a
support window, so the design serves a minority and degrades badly for everyone
else.

Hosting a version feed on the magus site. Still a network call, now to a domain
we control, and it commits a project with no revenue to keeping an aggregator
fresh forever.

Putting the policy in `magus.yaml`. Config merges a user-global tier beneath the
workspace, so a bound written in one person's private file would quietly gate
every workspace on their machine. It also offered no per-project granularity,
which a monorepo needs.

</details>

The surviving design is a version range you declare, compared against a version
magus already probes. It never learns which versions exist upstream and never
picks one. To find out when your range has gone stale, write that in your own
repo with `http`, `json`, and `semver`, the way `tools/audit.buzz` wraps the
advisory scanner. The binary supplies primitives. You supply knowledge about the
world.

## The line

Name a boundary before anyone wants to cross it, or naming it proves nothing.

magus will not select a version, install one, or move you to one. It compares
what ran against what you declared. An install, switch, or resolve verb under
`tools:` is the creep, and it will arrive with a good argument attached.

magus will not require a second toolchain to build your projects. No container
runtime, no language runtime to provision, no separate binary. The daemon carries
an asterisk; see below.

magus will not be recommended for install through a package manager belonging to
a toolchain it manages. Install a build tool with npm and you need a Node runtime
before you can run the thing managing your Node builds. When it breaks, you fix
it by upgrading the toolchain you were using magus to pin. Those failures are
oblique, hard to guard against, and there is rarely anywhere sensible to attach
an error explaining them. The same objection rules out `go install`. Anyone who
knows what they are doing will do it anyway; nobody should be pointed down that
path.

magus will not generate code your build depends on. Nothing it writes into your
repository has to exist or be current for `magus run build` to work. That promise
is narrower than "magus writes nothing into your repo", because it writes several
things; see below.

magus will not require an account or a subscription, and no capability sits
behind a paid tier. Nothing exists to upsell.

magus will not decide for you. It answers questions. You decide, or your agent
does.

## Where the claim is strained

The claim is that magus only reads a model it already had to build. Five places
strain it.

**It writes versioned artifacts into your repository.** `magus agent install`
writes agent skills into `.claude/skills/`, `.opencode/skills/`, and `AGENTS.md`,
each stamped with a magus-internal version. `magus graph verify` reports on them:

```text
agent skills (.claude/skills): up to date (skill v23, schema v7, content ace009cb3627)
```

`MAGUS.md` lands at your repo root. The git merge driver writes your tracked
`.gitattributes`. The Dagger criticism below applies here too.

magus claims that none of it gates a build: delete every one and `magus run
build` still works, delete Dagger's bindings and nothing compiles. That
distinction is thinner than it sounds. This repo's `generate` target regenerates
the skills and `ci` runs `generate` as a drift gate, so stale skills fail CI
here. Leaning on build-versus-CI is the move this page exists to catch.

**The daemon runs long, and one check requires it.** It ships inside the binary,
so you install nothing extra, and no build needs it. But `magus doctor` probes
bridge reachability and fails without a daemon running, which this repo's
CLAUDE.md already records as a known local failure. The console and the MCP
server need it too. "No second toolchain" holds for installation and holds less
firmly at runtime.

**The upgrade path runs on a server we operate.** `magus self update` fetches
`https://eli.gladman.cc/magus/public/release/index.json`. We sign the releases
and you opt into the check, and it remains infrastructure we control, in a
project that pitches not having any.

**The docs describe a `go install` path.** `docs/setup/mise.md` documents the
route the rule above says not to recommend, because you will find it anyway. It
carries a warning giving the structural reason. Documenting a route while telling
you to avoid it is a compromise, and a tension.

That page spent a long time discouraging `go install` for a cosmetic reason: it
cannot pass the `-ldflags` that stamp the version. Underneath, the command failed
outright on most clean machines. `internal/codec` gated the native xz and zstd
implementations on the `cgo` build tag, `CGO_ENABLED` defaults to 1 wherever a C
compiler exists, and an ordinary install then demanded `liblzma` and `libzstd`
headers through pkg-config before dying without naming magus or the fix. No
maintainer ever hit it, because maintainers have the headers. The native codec is
opt-in now.

**magus's own console needs a Node toolchain to build.** The tool that ships as
one statically linked binary contains a project requiring pnpm and esbuild. That
concerns developing magus rather than using it, and it reads as hypocrisy when
somebody else finds it first.

A sixth sits close to the line without crossing it. A version range is policy
living inside a build tool, and you can reasonably say pinning belongs to a
version manager. A version manager pins what to install; magus checks what ran.
Those diverge more often than you expect, and only the second can fail your build
with a message naming the cause. magus already held the answer.

The repo artifacts and the update endpoint are rules stated harder than the code
earns, so we narrowed the rules. The daemon and the console toolchain are
deliberate trades worth re-examining. The codec gate was simply a bug, fixed
separately once someone looked.

## Where others drew it

Some of these projects shaped magus directly. The disagreements are specific.

Nx comes up here because of proximity: it is what this project's author has used
most and most recently, so its edges are the memorable ones. Its project graph is
the closest prior art for affected sets. The disagreement is surface area. `nx
release` and `nx generate` do not read that graph, they are separate capabilities
with their own data models bolted to one CLI, and the plugin matrix multiplies
into more permutations than a team can support. You see the result as
inconsistency between corners and regressions in unrelated places, the cost of
being adequate at many things. Nx Cloud is an upsell, which shifts what the tool
optimizes for in ways you cannot see from inside a repo depending on it. The npm
install channel is the concrete case behind the rule above.

Dagger's SDK design is excellent and influenced magus's API. It costs a required
container runtime and generated bindings in your repository.

Bazel buys real hermeticity by owning your toolchain. The trade is coherent and
the cost is large.

Each of those took on a new capability. magus's additions read a model it already
had to build. Check that against the diff if you doubt it.

## Unsettled

The engine carries complexity the constraint camp avoids by limiting their
configuration language. Starlark is not Turing-complete on purpose; Nix and Dhall
made related trades. magus bets that a sandbox and a content-addressed cache can
contain what an expressive language does.

Nobody has settled that bet. A large enough body of magusfiles rots the way any
large program rots, and tooling discipline does not rule it out. If that happens,
this page was wrong about something load-bearing, and it should say so here.
