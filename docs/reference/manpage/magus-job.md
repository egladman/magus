---
title: magus job
generated_from: internal/cli/registry.go
description: "The POSIX child lifecycle over delegated work: fork declares a job, exec takes the lease on it in this checkout, exit returns it with its result, wait verifies that result, and run submits one of the daemon's own jobs."
tags: [cli, magus job, job, jobs, lease, agents, delegation]
---

# magus-job

Fork a job, take it, return it with its result, and verify that result

## Synopsis

**magus** job \<fork|exec|exit|wait|watch|run\> [flags]

## Description

Delegated work on the shell's own lifecycle. A JOB is the unit of work; a LEASE
is the grant one holder has on it: its write and read paths, plus the one check
it runs. A job is not a run: \`magus run\` executes a target with no job involved,
while a job's check and the daemon's maintenance each cause runs.

Two channels write the job store. The magus_job MCP tool is an agent's, this verb
is a person's, and they reach the same store and the same rules. One author per
JOB is the property that matters, and the store enforces it: a session holding a
lease may record the base it landed on, shrink its own write paths, end its own
job in fail or no_return, and fork a child of itself inside its own paths.
Everything else, widening a boundary and verifying a job included, is refused by name.

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

exec --vacate gives that marker up instead of writing one, so the checkout can
exec a different job. Refused while the job is still declared or running, since
walking away mid-flight would leave the checkout's next write ungraded; a job
already exited, one the store no longer carries, or no binding at all, all
vacate cleanly, which is what a checkout stuck on a lease nobody will ever wait
on needs.

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
store carries, and a recorded run of every declared completion gate that passed.
Evidence must have been captured after the job was declared, so an old green run
cannot satisfy new work. A target's own run policy bounds its execution; wait does
not add a competing wall-clock timeout. There is no field for whether the holder
thinks it passed. A job that verifies is recorded pass. Exit 1 is a status, naming
every rule that failed; exit 2 is magus unable to answer, which is a result that
would not decode, nothing filed and nothing piped in, or a job that would not
write. Whether the work is GOOD stays the reading of whoever forked it.

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

**--criteria** *string*
: What this job is for and what done means, as prose; the machine-checkable half is the completion gates (--check and every --gate-\* flag)

**--deny-paths** *string*
: A path inside the write paths this job may not write; repeatable or comma-separated

**--depends-on** *string*
: A job this one waits on; repeatable or comma-separated

**--gate-check** *\<id\>=\<target\> \<project\>*
: A further check this job must pass, as \`\<id\>=\<target\> \<project\>\`; repeatable

**--gate-paths** *\<id\>=\<glob\>[,\<glob\>...]*
: Files this job must have CHANGED, as \`\<id\>=\<glob\>[,\<glob\>...]\`, proven against its checkpoint; repeatable

**--gate-paths-absent** *\<id\>=\<glob\>[,\<glob\>...]*
: Files that must be GONE when the job is done, as \`\<id\>=\<glob\>[,\<glob\>...]\`; repeatable

**--gate-paths-present** *\<id\>=\<glob\>[,\<glob\>...]*
: Files that must EXIST when the job is done, as \`\<id\>=\<glob\>[,\<glob\>...]\`; repeatable

**--gate-symbol** *\<id\>=\<name\>[,\<name\>...]*
: Symbols whose definition this job must have CHANGED, as \`\<id\>=\<name\>[,\<name\>...]\`; repeatable

**--gate-symbol-absent** *\<id\>=\<name\>[,\<name\>...]*
: Symbols that must resolve NOWHERE when the job is done, as \`\<id\>=\<name\>[,\<name\>...]\`; repeatable

**--gate-symbol-present** *\<id\>=\<name\>[,\<name\>...]*
: Symbols that must resolve when the job is done, as \`\<id\>=\<name\>[,\<name\>...]\`; repeatable

**--gate-symbol-unreferenced** *\<id\>=\<name\>[,\<name\>...]*
: Symbols nothing may reference when the job is done, as \`\<id\>=\<name\>[,\<name\>...]\`; repeatable

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

**--timeout** *duration*
: Deny this job's writes once this long has passed since the fork (e.g. 45m, 2h); unset means no bound, unless magus.yaml sets jobs.default_timeout

**--write-paths** *string*
: A path this job may write; repeatable or comma-separated

### job exec options

**--base** *magus vcs checkpoint -o name*
: The base this checkout landed on, as \`magus vcs checkpoint -o name\` prints it (default: read from this checkout)

**--session** *string*
: The session taking the job, as this agent host names it. Several sessions in one checkout each hold their own lease; without it the binding is the whole checkout's

**--vacate**
: Give up the lease this checkout holds, so a later exec can take a different one. A no-op if it holds none; refused while the job is declared or running

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

### job rm options

**--force**
: Remove a row that already ended, destroying the record of what happened

## Subcommands

**fork**
: Declare one job, from flags or a JSON record on stdin

**exec**
: Take the lease on a job here, and record the base this checkout landed on

**exit**
: Return a job with its result, or abandon it

**wait**
: Verify the result a job was exited with

**watch**
: Follow what a job's holder is doing, until interrupted

**run**
: Submit one of the daemon's own jobs and return

**rm**
: Remove one job from the plan

## Examples

*Declare a job*

```sh
magus job fork session-load/core --write-paths internal/sessions/sessions.go --check 'test internal/sessions'
```

*Declare it from a record*

```sh
magus job fork --stdin < job.json
```

*Take it in this checkout*

```sh
magus job exec session-load/core
```

*Give up this checkout's binding*

```sh
magus job exec --vacate
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

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

