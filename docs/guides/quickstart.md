---
title: Quick start
description: Everything you need to be productive with magus on one page - install, first target, the output flags, the agent skills and the guard hook - each linking to the page that goes deeper.
tags: [getting-started, install, quickstart, agents, skills, cli, reference]
---

# Quick start

One page, densely packed, for someone who wants to be productive now and read
properly later. Every section links to the page that goes deeper.

If you want the guided, explain-as-you-go version instead, read
[Getting started](getting-started.md). If you want to try magus without
installing anything, the [playground](../playground.html) runs the same engine in
your browser.

## Install

```sh
curl --proto '=https' --tlsv1.2 -sSf https://eli.gladman.cc/magus/install | sh
```

Prefer to read it first (recommended, and the same two lines the script prints):

```sh
curl --proto '=https' --tlsv1.2 -sSf https://eli.gladman.cc/magus/install -o install.sh
less install.sh
sh install.sh
```

The installer verifies a checksum against a signed release. Package managers,
containers, CI setup actions, and building from source are all on
[Download](download.md). To pin a version in CI, use the `setup-magus` action
rather than curling into a runner.

Check it worked, and check the workspace is sane:

```sh
magus --help
magus doctor        # config, cache, tools, cycles, guard binary
```

## The one thing to understand

A **target** is a named operation (`build`, `test`, `lint`, `format`,
`generate`, `ci`) declared as a function in a `magusfile.buzz`. A **project** is
a directory that owns one. A **spell** binds a toolchain (go, ts, rs, py, ...) so
a project gets that toolchain's operations without writing them.

magus knows each target's declared inputs and outputs, so it caches results and
can compute which projects a change actually affects. That is the whole value
proposition, and it is also why running the raw tool underneath defeats it.

Deeper: [Targets](../concepts/targets.md), [Workspace](../concepts/workspace.md),
[Spells](../concepts/spells.md), [Cache](../concepts/cache.md).

## Your first five commands

```sh
magus init                  # bootstrap a magusfile in an existing repo
magus ls                    # every project, its spell, sources, outputs, deps
magus describe targets      # every target; -o name for bare names
magus run test              # run a target (cwd project, or all from the root)
magus affected ci           # the gate: full pipeline over what your diff reaches
```

`magus affected ci` is the one to run before you call work done. It runs over
every project your change reaches, including ones you never edited.

Deeper: [Getting started](getting-started.md), [CLI reference](../reference/cli.md).

## Scoping: magus is CWD-relative

A bare `magus run` acts on the project holding your current directory, or the
whole workspace from the root. Do not assume the root. Scope explicitly:

```sh
magus run test web          # name the project (positional, after the target)
magus run go::go-test web   # one spell op, when a whole target is too broad
magus run test -- -run TestX  # args after -- are forwarded to the tool
magus where web             # resolve a fuzzy project name to its path
```

Add `--dry-run` to any of these to print the exact commands without running them.

## Output control

magus has output flags, so you never need to pipe its output through `grep`,
`head`, or `awk`. Every one of these works on every command:

| flag | does |
| --- | --- |
| `-s` / `--silent` | a pass prints a result line plus an output ref; a failure adds a bounded tail |
| `-q` / `--quiet` | drops progress, keeps errors and the failing project's full output |
| `-o json` / `yaml` / `jsonl` | machine-readable, `schema_version`-stamped |
| `-o name` | bare identifiers, one per line |
| `-o template=<go-template>` | project exactly the fields you want |
| `-v` / `-vv` / `-vvv` | more log verbosity |
| `--tee <file>` | mirror structured output to a file |

When a target fails it mints an output ref (`ref1a2b3c`). Fetch the full log with
`magus query output ref1a2b3c` instead of re-running the target to see the error
again.

Deeper: [Logging](../reference/logging.md), [CLI reference](../reference/cli.md).

## Set up your coding agent

magus ships skills for coding agents, and a guard hook that stops an agent
bypassing the workspace. Install the skills into whichever directories your host
reads:

```sh
magus agent install .claude/skills      # Claude Code
magus agent install .agents/skills      # the cross-host convention
magus agent install .opencode/skills    # opencode
magus agent install-agents-md           # the managed block in AGENTS.md
```

Any destination your shell can reach works via the tar form, which is also how
you install outside the working tree:

```sh
magus agent install --tar | tar -xf - -C ~/.config/opencode/skills
```

### `--simple`: the short permutation

Every skill ships in two hand-authored permutations from one source body:

```sh
magus agent install .claude/skills --simple
```

The default carries the rationale behind each step. `--simple` withholds it,
keeping the imperative steps, for a reader that infers the why. Prefer it for a
capable model where context budget matters, and the full form when you want the
agent to be able to justify what it is doing, or when you are onboarding a human
to the same conventions.

Both permutations share ONE content digest, so they version together: a magus
upgrade makes both stale at once, never one silently. Check with:

```sh
magus graph verify
```

Deeper: [Agent skills](../reference/skills/index.md) shows every skill in both
forms with its size, and [Agents](integrations/agents.md) covers host wiring.

### The guard hook

The guard reads one command an agent is about to run and returns deny, advise, or
pass. It parses the shell rather than pattern-matching it, so a command cannot
evade the guard by adding an environment prefix or a shell indirection:

```sh
magus hook -- go test ./...                 # deny: magus run test covers this
magus hook -- env -u GOROOT go test ./...   # deny: same command, prefix peeled
magus hook -- magus run test                # pass
```

It denies on three triggers: what cannot be undone (whole-tree VCS operations),
what WRITES into the working tree (codegen, formatters, build output), and what
has an exact magus equivalent. Everything else it explains or ignores.

Wire it into your host with the ready-made scripts (Claude Code, Codex, Cursor,
opencode, Amp, Zed are all covered):
[Agents: guard hooks](integrations/agents.md#guard-hooks).

Point it at a real binary. If the guard cannot find one it says so loudly, and
`magus doctor`'s **guard binary** check names the binary a hook would run and
fails when it is older than your working tree - because a stale guard enforces
stale rules while looking perfectly healthy.

## Where to go next

| you want | read |
| --- | --- |
| the guided walkthrough | [Getting started](getting-started.md) |
| every install method | [Download](download.md) |
| what a target is, really | [Targets](../concepts/targets.md) |
| how caching decides a hit | [Cache](../concepts/cache.md) |
| binding a toolchain | [Spells](../concepts/spells.md) |
| sandboxing and what it enforces | [Sandbox](../concepts/sandbox.md) |
| writing magusfile logic | [Buzz reference](../reference/buzz/index.md) |
| querying the knowledge graph | [Knowledge](../concepts/knowledge.md) |
| a diagnostic code you hit | [Diagnostics](../reference/diagnostics.md) |
| running the daemon and console | [Daemon](integrations/daemon.md), [Console](../reference/console.md) |
| CI wiring | [CI providers](../concepts/ci-providers.md) |
| when something is wrong | [Debugging](debugging.md), [FAQ](../reference/faq.md) |

Every flag and target set differs per workspace and magus version, so trust
`magus describe targets`, `magus describe target <name>`, and `magus <verb> -h`
over anything written down - including this page.
