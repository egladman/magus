---
title: magus ledger
generated_from: internal/cli/registry.go
description: "Read the per-repository lease ledger: the leases an orchestrating agent or a person declared, as a tree, plus the worker brief for any one of them, the row-declaring write, and the acceptance check for a worker's report."
tags: [cli, magus ledger, ledger, leases, agents, delegation]
---

# magus-ledger

Read the lease ledger, declare a row, and grade a worker's report

## Synopsis

**magus** ledger [ls|brief \<lease-id\>|register \<lease-id\>|accept \<lease-id\>] [flags]

## Description

Read and write the lease ledger: one row per lease, rendered as a tree of
parents and the leases they handed out.

Two channels write it. The magus_ledger MCP tool is an agent's, this verb is a
person's, and they reach the same store and the same rules. One author per ROW is
the property that matters, and the store enforces it: a session acting under a
lease may register the base it landed on, shrink its own write paths, end its own
row in fail or no_return, and declare a child of itself inside its own paths.
Everything else, widening a lane and accepting a row included, is refused by name.

The rows are kept per repository rather than per checkout, so an orchestrator
declaring a plan in one worktree and a worker reading it in another see the same
book.

brief renders one lease's worker brief: the row's own goal and acceptance
criteria, its write and deny paths, the knowledge graph's blast radius for
each write path it can resolve, the single validation target that lease is
allowed to run, its dependencies, and the bootstrap commands the worker starts
with, each with the reason it is there. It carries no rules: the guard states
those at the moment a command meets one. It also carries what the WORKSPACE knows
and the row's author may not have written down: the projects the write paths
reach, the declared output globs that land
inside them, the paths a sibling lease is holding, the build inputs and workspace
config that have one owner, and the projects that change alongside the leased
ones without declaring a dependency. It is context and never a verdict, the same
shape magus diff --prompt has, with one refusal: a row whose validation is the ci
gate, or a target that chains to it, is not briefed at all. The gate runs once,
in the orchestrator's tree, after every unit lands.

register declares one row, from flags for the one-row case or from a JSON record
on --stdin. It replaces the row it names rather than merging into it, which is
the difference between a person declaring what a lease IS and an agent advancing
one field of a live row. Every path flag is repeatable or comma-separated, and an
empty segment is refused rather than dropped. There is no --state: this declares a
new row, and a row nobody has picked up is declared.

accept grades what comes back. It reads a worker's report as JSON on stdin and
grades EVIDENCE, not claims: every changed path inside the declared write paths
and outside the denied ones, a change set that is not empty on a row that
writes, descendants the plan carries, and an output ref that resolves to a passing
run of that row's own validation. There is no field for whether the worker thinks
it passed. A row that passes is recorded pass. Exit 1 is a verdict, naming every
rule that failed; exit 2 is magus unable to answer, which is a report that would
not decode, an output store that would not open, or a row that would not write.
Whether the work is GOOD stays the orchestrator's reading.

### ledger register options

**--check** *\<target\> \<project\> [-- args]*
: The one check this lease runs, as \`\<target\> \<project\> [-- args]\` (the \`magus run\` is implied)

**--checkpoint** *magus vcs checkpoint -o name*
: The working state this lease is handed, as \`magus vcs checkpoint -o name\` prints it

**--deny-paths** *string*
: A path inside the lane this lease may not write; repeatable or comma-separated

**--depends-on** *string*
: A lease this one waits on; repeatable or comma-separated

**--goal** *string*
: The goal and its observable acceptance criteria

**--model** *string*
: The model the work was matched to

**--parent** *string*
: The lease this one is handed out under

**--read-only**
: A lease that gathers evidence and writes nothing

**--read-paths** *string*
: A path whose projects this lease may read; repeatable or comma-separated (additive: the written paths are readable already)

**--schema**
: Print the JSON schema a row must satisfy, and exit

**--stdin**
: Read one row as JSON on stdin instead of taking it from flags

**--write-paths** *string*
: A path this lease may write; repeatable or comma-separated

### ledger accept options

**--schema**
: Print the JSON schema a report must satisfy, and exit

**--stdin**
: Read the worker's report from stdin (required: nothing is read without it)

## Subcommands

**ls**
: Print the declared leases as a tree, with the overlapping pairs

**brief**
: Print one lease's worker brief

**register**
: Declare one lease row, from flags or a JSON record on stdin

**accept**
: Grade a worker's report, read as JSON on stdin, against its row

## Examples

*Read the declared plan*

```sh
magus ledger
```

*Read it as records*

```sh
magus ledger -o json
```

*Brief one worker*

```sh
magus ledger brief session-load/core
```

*Declare a lease*

```sh
magus ledger register session-load/core --write-paths internal/sessions --check 'test internal/sessions'
```

*Declare it from a record*

```sh
magus ledger register --stdin < row.json
```

*Grade what it returned*

```sh
magus ledger accept session-load/core --stdin < report.json
```

*Print the report schema*

```sh
magus ledger accept --schema
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

