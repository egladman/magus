---
title: magus job
generated_from: internal/cli/registry.go
description: "The POSIX child lifecycle over delegated work: fork declares a job, exec takes the lease on it in this checkout, exit returns it with its result, wait verifies that result, and run submits one of the daemon's own jobs."
tags: [cli, magus job, job, jobs, lease, agents, delegation]
---

# magus-job

Fork a job, take it, return it with its result, and verify that result

## Synopsis

**magus** job \<fork|exec|exit|wait|run\> [flags]

## Description

Delegated work on the shell's own lifecycle. A JOB is the unit of work; a LEASE
is the grant one holder has on it: its write and read lanes, plus the one check
it runs.

Two channels write the job store. The magus_job MCP tool is an agent's, this verb
is a person's, and they reach the same store and the same rules. One author per
JOB is the property that matters, and the store enforces it: a session holding a
lease may record the base it landed on, shrink its own write paths, end its own
job in fail or no_return, and fork a child of itself inside its own paths.
Everything else, widening a lane and verifying a job included, is refused by name.

Jobs are kept per repository rather than per checkout, so a session forking in one
worktree and a holder working in another see the same set.

fork declares one job, from flags for the one-job case or from a JSON record on
--stdin. It replaces the job it names rather than merging into it, which is the
difference between a person declaring what a job IS and an agent advancing one
field of a live one. Every path flag is repeatable or comma-separated, and an
empty segment is refused rather than dropped. There is no --state: this declares a
new job, and one nobody has taken is declared.

exec takes the lease on a job in this checkout. It writes the marker every
lease-scoped rule reads, and records the base this tree actually landed on beside
the checkpoint the job was handed, with the divergence between them as a fact
rather than a refusal. Two acts under one verb, because splitting them left the
base unrecorded on every job anybody took by hand.

exit returns a job with its result, FILED ONTO THE JOB so whoever waits on it
reads the same record from any checkout. The run behind the result's output ref is
resolved in the holder's own checkout and its record filed alongside, because an
output store belongs to a cache dir and nobody else can reopen it. Do the work
first, then file the result: it is a record of what happened, not a form to fill
in while you are still deciding what to do. With no --stdin the job is abandoned
and recorded no_return, which is not the same as returning it and failing.

wait verifies what comes back. It reads the filed result and verifies EVIDENCE,
not claims: every changed path inside the declared write paths and outside the
denied ones, a change set that is not empty on a job that writes, descendants the
store carries, and a recorded run of that job's own check that passed. There is no
field for whether the holder thinks it passed. A job that verifies is recorded
pass. Exit 1 is a status, naming every rule that failed; exit 2 is magus unable to
answer, which is a result that would not decode, nothing filed and nothing piped
in, or a job that would not write. Whether the work is GOOD stays the reading of
whoever forked it.

run submits one of the daemon's own jobs, the housekeeping magus does for itself,
and returns. It is a no-op when no persistent daemon is running, so a VCS hook can
call it unconditionally.

Reading is elsewhere, on the verbs that read everywhere else: magus ls jobs lists
them and magus describe job prints one job's terms.

### job fork options

**--check** *\<target\> \<project\> [-- args]*
: The one check this job runs, as \`\<target\> \<project\> [-- args]\` (the \`magus run\` is implied)

**--checkpoint** *magus vcs checkpoint -o name*
: The working state this job is handed, as \`magus vcs checkpoint -o name\` prints it

**--deny-paths** *string*
: A path inside the lane this job may not write; repeatable or comma-separated

**--depends-on** *string*
: A job this one waits on; repeatable or comma-separated

**--goal** *string*
: The goal and its observable acceptance criteria

**--model** *string*
: The model the work was matched to

**--parent** *string*
: The job this one is forked from

**--read-only**
: A job that gathers evidence and writes nothing

**--read-paths** *string*
: A path whose projects this job may read; repeatable or comma-separated (additive: the written paths are readable already)

**--schema**
: Print the JSON schema a job must satisfy, and exit

**--stdin**
: Read one job as JSON on stdin instead of taking it from flags

**--write-paths** *string*
: A path this job may write; repeatable or comma-separated

### job exec options

**--base** *magus vcs checkpoint -o name*
: The base this checkout landed on, as \`magus vcs checkpoint -o name\` prints it (default: read from this checkout)

### job exit options

**--schema**
: Print the JSON schema a result must satisfy, and exit

**--stdin**
: Read this job's result from stdin; without it the job is abandoned

### job wait options

**--schema**
: Print the JSON schema a result must satisfy, and exit

**--stdin**
: Read the result from stdin instead of from the job, for one that was never filed

## Subcommands

**fork**
: Declare one job, from flags or a JSON record on stdin

**exec**
: Take the lease on a job here, and record the base this checkout landed on

**exit**
: Return a job with its result, or abandon it

**wait**
: Verify the result a job was exited with

**run**
: Submit one of the daemon's own jobs and return

## Examples

*Declare a job*

```sh
magus job fork session-load/core --write-paths internal/sessions --check 'test internal/sessions'
```

*Declare it from a record*

```sh
magus job fork --stdin < job.json
```

*Take it in this checkout*

```sh
magus job exec session-load/core
```

*Return it with its result*

```sh
magus job exit session-load/core --stdin < result.json
```

*Verify what came back*

```sh
magus job wait session-load/core
```

*Print the result schema*

```sh
magus job exit --schema
```

*Submit a daemon job*

```sh
magus job run sync-graph
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

