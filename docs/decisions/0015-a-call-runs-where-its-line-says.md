---
title: "ADR 0015: a call runs where its line says"
order: 15
description: Host shells differ in where a subagent's call starts, and the lease rules read only the directory the host reported. This record has the guard place each magus call by its own line, denies a holder's call that resolves outside its checkout, and leaves the CLI's root rules alone.
tags: [adr, decision, guard, leases, agents, checkouts]
status: proposed
date: 2026-10-22
---

# ADR 0015: a call runs where its line says

## Context

A worker forked with `job fork --checkout` owns a linked checkout, but the host decides
where its shell starts. A subagent's call can begin in the parent's directory, and a `cd`
does not persist from one Bash call to the next
([Bash tool behavior](https://code.claude.com/docs/en/tools-reference#bash-tool-behavior)).
The lease rules read one location, the directory the host's envelope named, so a worker
that typed `./magus run go-test .` was graded, and its run was cached and written, in the
parent's tree. The same envelope made a holder's `cd <checkout> && magus affected ci`
look like a gate from the wrong checkout.

Shells also differ by host: Codex, Cursor and OpenCode each report a working directory in
their own way, and some report none.

## Decision

### The guard places each magus call by its line

A shared resolver answers where a magus call runs: the directory the line has reached
through a leading `cd`, moved by the call's own `-C`, `--root` and `--root=`, then the
workspace root holding it. One that cannot be read without running the line (a variable,
a substitution, `~`, a call inside a conditional or loop) is placed where the host
started, which is what every rule did before.

| Item                                                                                                          | State |
| ------------------------------------------------------------------------------------------------------------- | ----- |
| `placeMagusCalls` places every magus call on a line (`internal/guard/lease.go`)                               | done  |
| `gatesInHeldCheckout` and the Buzz `gatesInHeldCheckout` judge a holder's gate where the call runs            | done  |
| `actingIntegrator` and `denyWorkerCheckOnly`'s row judge read the workspace the line runs in                  | done  |
| `background.go`, `bootstrap.go` and the brief judge read the workspace each line runs in                      | done  |
| `lease-checkout` denies a holder's magus call that resolves outside its row's `Worktree.Root`, default deny   | done  |
| `magusCdTargets` (`shell.go`) stays: it resolves same-line variable assignments and serves `sibling-checkout` | done  |

### `lease-checkout`

The holder of a row with a standing worktree is denied a magus call that resolves outside
that worktree. The refusal quotes the line to run: `<Root>/magus -C <Root> <args>`, with
the call's own `-C` dropped. A plan, a dry run, a graph and help pass. The rule is silent
with no lease, a row with no worktree, the root session, and a line that runs no magus.

### The CLI's root rules do not change

`-C`, `--root` and the nearest `magus.yaml` resolve the workspace as before. Only the
guard reads the line.

### Principles checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): relied on. A call the line
  cannot place falls back to the envelope and the rule says nothing, rather than
  guessing a checkout.
- [A record of refusals](../design/refusals.md): relied on. The
  lease-implied root is added as a refusal.

## Alternatives

- **Rewriting the command through the host's `updatedInput`.** Lost: the guard would
  edit what the model asked for, and the outer harness's own rewriting stays outside
  magus. The deny names the line to run and leaves the edit to the caller.
- **A `MAGUS_ROOT` environment variable, or a CLI root implied by the lease.** Lost: a
  run's root would depend on state the line does not show, which is a silent default. A
  reader of the transcript could not tell which tree a command ran in. Reopen if a host
  can carry a working directory per subagent.
- **Judging with the binary the line names.** Lost: there is one build per base, so the
  binary beside the checkout is the one that judges it already, and a second choice
  would run a different build over the same tree.
- **A `WorktreeCreate` hook template.** Lost: host-only, so it does not reach the other
  hosts. Reopen with a mapping from a host's spawn to a job.

## Consequences

- A worker names its checkout on every magus line. The leases guide and the Claude Code
  page say so.
- A `cd <checkout> && magus affected ci` passes for the holder from any directory, and a
  gate aimed at another checkout is refused.

## Open questions

- Whether a host reports a subagent's starting directory reliably enough to drop the
  fallback to the envelope.
