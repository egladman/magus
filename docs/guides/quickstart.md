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
[Download](../setup.md). To pin a version in CI, use the `setup-magus` action
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

| flag                         | does                                                                          |
| ---------------------------- | ----------------------------------------------------------------------------- |
| `-s` / `--silent`            | a pass prints a result line plus an output ref; a failure adds a bounded tail |
| `-q` / `--quiet`             | drops progress, keeps errors and the failing project's full output            |
| `-o json` / `yaml` / `jsonl` | machine-readable, `schema_version`-stamped                                    |
| `-o name`                    | bare identifiers, one per line                                                |
| `-o template=<go-template>`  | project exactly the fields you want                                           |
| `-v` / `-vv` / `-vvv`        | more log verbosity                                                            |
| `--tee <file>`               | mirror structured output to a file                                            |

When a target fails it mints an output ref (`out1a2b3c`). Fetch the full log with
`magus query output out1a2b3c` instead of re-running the target to see the error
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
```

If your host reads `AGENTS.md` instead of a skills directory, install prints the
magus block for you to paste in. magus does not write that file - it is yours,
and it stays quiet once your copy is current. For a whole starter file to own:

```sh
magus agent sample                      # prints an AGENTS.md to adapt; never writes
```

Any destination your shell can reach works via the tar form, which is also how
you install outside the working tree:

```sh
magus agent install --tar | tar -xf - -C ~/.config/opencode/skills
```

### Two permutations, both installed

Every skill ships in two hand-authored permutations from one source body, and
install writes both. There is no flag to pick between them.

The primary entry is the SHORT form: the enumeration dropped, the judgment kept,
for the most capable readers - the ones that can re-derive the steps from the
tool surface but not which failures are silent. It is the one always loaded, so
it is the one whose size every session pays for.

Beside it goes an always-full `<skill>-full` twin, loaded only when asked for by
name. Reach for that name when you hand work to a smaller model, or when you
want the rationale behind a step yourself. The short form bets its reader can
re-derive what it drops; the twin is there for every reader who did not make
that bet.

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
printf '%s' 'go test ./...' | magus hook -o name               # deny: magus run test covers this
printf '%s' 'env -u GOROOT go test ./...' | magus hook -o name # deny: same command, prefix peeled
printf '%s' 'magus run test' | magus hook -o name              # pass
```

It denies on four triggers: what cannot be undone (whole-tree VCS operations),
what WRITES into the working tree (codegen, formatters, build output), what has
an exact magus equivalent, and what breaks a provenance guarantee (a note).
Everything else it explains or ignores.

Wire it into your host with the ready-made scripts:
[The guard](integrations/agents/guard.md), with a setup page per host behind
[Agents](integrations/agents.md).

Point it at a real binary. If the guard cannot find one it says so loudly, and
`magus doctor`'s **guard binary** check names the binary a hook would run and
fails when it is older than your working tree - because a stale guard enforces
stale rules while looking perfectly healthy. The **guard wiring** check answers
a different question: whether anything actually invokes it. It runs a canary
command through the resolved binary and inventories every host hook config it
finds, advising when none exists (correct rules, nothing asking them) and
failing when a config points at a template file that is stale or missing.

## Where to go next

| you want                        | read                                                                 |
| ------------------------------- | -------------------------------------------------------------------- |
| the guided walkthrough          | [Getting started](getting-started.md)                                |
| every install method            | [Download](../setup.md)                                              |
| what a target is, really        | [Targets](../concepts/targets.md)                                    |
| how caching decides a hit       | [Cache](../concepts/cache.md)                                        |
| binding a toolchain             | [Spells](../concepts/spells.md)                                      |
| sandboxing and what it enforces | [Sandbox](../concepts/sandbox.md)                                    |
| writing magusfile logic         | [Buzz reference](../reference/buzz/index.md)                         |
| querying the knowledge graph    | [Knowledge](../concepts/knowledge.md)                                |
| a diagnostic code you hit       | [Diagnostics](../reference/diagnostics.md)                           |
| running the daemon and console  | [Daemon](integrations/daemon.md), [Console](../reference/console.md) |
| CI wiring                       | [CI providers](../concepts/ci-providers.md)                          |
| when something is wrong         | [Debugging](debugging.md), [FAQ](../reference/faq.md)                |

Every flag and target set differs per workspace and magus version, so trust
`magus describe targets`, `magus describe target <name>`, and `magus <verb> -h`
over anything written down - including this page.
