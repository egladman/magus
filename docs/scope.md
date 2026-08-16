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

**One of those four is being reversed, and it should be named rather than
quietly dropped.** A signed registry of release and end-of-life data, hosted on
the magus site and fetched only when you ask for it, is the third rejection: it
is a network call to a domain we control, and it does commit a project with no
revenue to keeping an aggregator fresh. Neither objection has stopped being
true. What changed is that the failure mode is now visible instead of silent:
staleness is measured from a `generated_at` inside the signed file, so an
aggregator nobody is maintaining reports itself as old rather than serving
confident stale dates. The file is keyed by upstream product slug and carries no
magus concepts, so mirroring it is a copy rather than a port, and a mirror is
what you reach for when you would rather not depend on us. Declining is
`enabled: false`, and a declined install is silent forever rather than nagged.

The other three stand, and the registry does not smuggle them back:

- **No release-feed type in the binary.** The registry is data magus fetches, not
  a concept it implements. It republishes a third party's fields; it does not
  decide what counts as a release or what LTS means, and there is nothing in the
  binary that would have to answer those questions.
- **The data is still sparse.** Around five of the eighteen tools here publish a
  support window, and a registry does not create the other thirteen. It reports
  `-` for them. Serving a minority was only a fatal objection when the design
  degraded into a wrong answer; reporting "unknown" for most tools is honest.
- **The policy still is not in config.** What a source-of-truth URL is gets
  configured; what version range your project accepts does not, and stays where
  it was. Nothing the registry reports fails a build - it is a column in a
  report and a doctor line, never a gate.

Read what a signature does and does not prove before relying on it: it says the
file is the one our pipeline published, unmodified. It does not say any date in
it is correct. magus did not author this data and cannot verify it.

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

## The container question

"No container runtime" is one clause of one rule above, and it is the clause
with the best argument against it, so it gets its own section.

The argument is Dagger's, and it is a good one. Run every step in a container
against a pinned image and the environment stops being a variable. No more
"works on my machine": the machine is the same machine. That is a real property,
and it removes an entire category of support burden.

**A container gives you environment reproducibility, not hermeticity.** A pinned
image fixes what is installed. Inside it, a step can still reach the network,
read the clock, resolve a floating tag, or depend on filesystem ordering.
Nothing fails a build there because a step read a file it never declared. That
is Bazel's guarantee, and containerizing does not supply it. The certainty a
container produces is partly a feeling, and the feeling is
what makes the runtime dependency seem cheap.

**The dependency is disqualifying at this level specifically.** A task
orchestrator is the thing you reach for before anything else works. If it needs
a container runtime, it cannot be used to install or check that runtime; it is
unavailable on a locked-down laptop, a rootless runner, or an air-gapped
builder; and when the daemon is not up, the failure arrives oblique, far from
the cause, with nowhere sensible to attach an error explaining it. That is the
same shape as the rule about package managers above - a tool that arrives
through the thing it is meant to orchestrate has put itself downstream of it.
One rule, two instances.

So magus pursues determinism without the runtime, through mechanisms that need
nothing installed: inputs are declared and hashed, the child environment is
rebuilt from an allowlist rather than inherited, every tool's version is part of
the cache key, and a declared version window fails the build when what
ran falls outside it. Where a container normalizes the world, magus describes it
precisely and notices when the description stops matching.

That is a harder route with more moving parts, and being specific about the
remaining gap matters more than the claim does. Undeclared **file reads** are
caught only by the [sandbox](concepts/sandbox.md), which is off by default and
has no kernel layer on macOS - so on the machine most of this is written on, an
undeclared read succeeds silently. **Network egress is not confined at all.**
Until both close, magus's determinism is "the inputs you declared are hashed and
the environment is scrubbed", which is weaker than what a container gives you on
the filesystem axis and stronger on the version axis. Weigh it that way when
you compare the two.

One thing this page must not imply, because the wording invites it: **magus has
no opt-in container isolation, and the `container` charm is not it.** In this
repo `magus run build:container` selects a different _artifact_ - it builds and
signs an image instead of a host binary - and the build itself still runs on the
host, unconfined. That is container-grade packaging, not container-grade
isolation, and they are different products. Reading the charm as "the Dagger
guarantee, available per run" is a misreading this section previously
encouraged.

So the honest position is narrower than "you can have it when you want it": if
you need every step to run in a fixed environment, magus does not offer that at
any setting, and a container-native runner is the better tool. What magus offers
instead is a precise description of the environment plus a build that fails when
what ran falls outside what you declared. That is a different bet, not a cheaper
version of the same one.

## The knobs

The test above governs verbs. Options need their own, because they are the other
way a tool loses its shape, and the cheaper one - nobody blocks a pull request
over one more setting.

An option is not additive. Each independent switch doubles the number of states
the tool can be in, and the states nobody thought about are where the
bugs live, because no one wrote a test for a combination no one imagined. A
configuration surface large enough to be flexible is large enough that its author
cannot enumerate what it does. You have met the result: a build that works on one
machine, a setting three people cargo-culted from a blog post, and a maintainer
who cannot tell you what turning it off would break.

So the default is the product. Zero configuration is the feature - not because
there are no knobs, but because **you should never need one to get correct
behavior**. magus is opinionated where an opinion prevents a footgun: one
required target name, four reserved charms, `skip_cache` demanding a reason
string rather than a boolean, no fallback chain when a secret will not resolve.
Each of those removes states rather than adding them.

The honest numbers, because this is the section where a claim like that gets
tested. `magus.yaml` accepts about a hundred keys, container and leaf together,
and magus binds 63 of them to command-line flags. That is not a small surface,
and calling it zero configuration would be a lie.

What the claim rests on is the second number. magus's own `magus.yaml`, for a
ten-project polyglot repo that publishes containers, signs releases and runs a
sharded CI pipeline, sets four things:

```yaml
default_charms: [rw]
sandbox: { env: { passthrough: ["GO*"] } }
cache: { remote: { trusted_keys: ["..."] } }
required_version: ">= 0.4.0"
```

Three of those four are facts about this repository that no default could
supply - a trust key, an environment passthrough, a version floor. Only
`default_charms` is a preference. Every other key exists for a workspace whose
situation we did not anticipate, and the measure of whether that is discipline or
sprawl is whether we reach for them ourselves.

The rule that follows: **a new option must remove a failure, not enable a
preference.** If the answer to "what happens if I set this wrong" is "your build
is subtly different and nothing says so", it is not an option, it is a trap with
a default.

Where this is strained: an option nobody can find is worse than one that does not
exist, because the escape hatch is real and the person who needs it is told it is
not. `cache.include.os.enabled` and `cache.include.arch.enabled` decide whether
host OS and architecture key every cache entry. Both are real, both have
environment variables, and neither appears in [the cache-key reference](concepts/cache.md) - which instead enumerates the hashed fields and
says nothing else reaches the hash. Anyone comparing a laptop's output reference
against CI's is looking at this, and the page that would tell them says
the inputs do not exist.

## Where the claim is strained

The claim is that magus only reads a model it already had to build. Five places
strain it.

**It writes versioned artifacts into your repository.** `magus agent install`
writes agent skills into the directories you name (`.claude/skills/`,
`.agents/skills/`, `.opencode/skills/`), each stamped with a magus-internal
version. It does NOT write `AGENTS.md`: that file is yours, so install prints
the managed block for you to paste. `magus graph verify` reports on both
surfaces, the pasted block included:

```text
agent skills (.claude/skills): up to date (skill v33, schema v8, content 26f270bc4a34)
agent skills (AGENTS.md): up to date (skill v33, schema v8, content 26f270bc4a34)
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
outright on most clean machines. `internal/compress` gated the native xz and zstd
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

The agent-harness world drew the line at the opposite extreme, and the contrast
is worth stating because magus keeps being read as a member of that category.
deepseek-harness makes every module a plugin - the model adapter, the tool
registry, the session log, the agent loop itself - so there is no core code at
all. For a harness that is a defensible bet: a harness is an orchestration shell,
its behavior is supposed to be yours, and hackability is the product. magus made
the opposite bet for the opposite reason. A build tool's product is its verdicts:
the cache saying a replay is honest, the drift gate saying generated output
matches source, `ci` stripping the write charms whether or not anyone remembered.
A pluggable verdict is not a verdict. So the engine, the cache, the graph schema,
and the guard's evaluation are sealed on purpose, and every extension point magus
does have - spells, the magusfile, charms, skills, config - is a DECLARATION the
sealed engine evaluates, auditable in a diff the way code loaded at startup never
is. The test for a proposed extension seam: it may change what magus does, never
what a verdict means. And one seam is absent deliberately rather than sealed:
there is no model adapter, because magus never calls a model - a fact this page
would rather state than have inferred.

## Unsettled

The engine carries complexity the constraint camp avoids by limiting their
configuration language. Starlark is not Turing-complete on purpose; Nix and Dhall
made related trades. magus bets that a sandbox and a content-addressed cache can
contain what an expressive language does.

Nobody has settled that bet. A large enough body of magusfiles rots the way any
large program rots, and tooling discipline does not rule it out. If that happens,
this page was wrong about something load-bearing, and it should say so here.
