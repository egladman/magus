---
title: magus agent
generated_from: internal/cli/registry.go
description: Render agent skills, adapt a user-owned harness, or review recurring guard feedback; it never writes the AGENTS.md you own.
tags: [cli, magus agent, skills, agents, AGENTS.md, install, harness]
---

# magus-agent

Manage skills, harnesses, and agent feedback

## Synopsis

**magus** agent \<install|harness|starter|adoption\> [flags]

## Description

Render the agent skills embedded in this binary and write or stream them
into named destinations (\<skills-dir\>).

magus never writes your AGENTS.md. That file is yours, and an installer that
edits a file you own leaves bytes you did not write and cannot audit. So
install PRINTS the managed magus block for you to paste, and only when your
AGENTS.md is missing it or is carrying a stale one. sample prints a starter
AGENTS.md to stdout for you to own and tweak, and never writes a file.

harness applies, removes, or verifies harnesses selected with
magus\\harness.provider (several hosts are fine when you bounce between LLM
tools) or a JSON descriptor: apply merges opaque host-config fragments the
descriptor already names, remove deletes only those same fragments (a user's
own hooks beside them are untouched, and nothing is asked for confirmation -
pass --dry-run to preview one first), and verify actually runs the wired guard
command against a synthetic event rather than trusting its mere presence in
the config. Omit --id to act on every magusfile-wired provider. Guard feedback
that keeps recurring is doctor's recurring-guard-denials check, not a verb here.

agent is a pure data generator, which is what makes --tar the general
answer: it streams a tar archive to stdout, so skills can be installed
anywhere a shell can reach. The write-to-disk form exists for the in-repo,
paths-relative-to-\<dir\> case. Absolute destinations are refused unless
--global is set, so magus cannot silently write outside the working tree.

adoption reads shell commands, one per line, from stdin or from
--commands \<file\>, and reports how often the graph was reached versus a raw
text search. -o json emits the report as one object keyed total, graph_verbs,
text_searches, search_of_source, search_of_prose, file_reads, magus_runs,
other, and top_symbol_greps. Each top_symbol_greps entry carries pattern,
count, and run - the graph command to try for that pattern, routed by its
shape: magus query for a diagnostic code or a Buzz op, which magus refs
(compiled-language symbols only) would miss, and magus refs otherwise. The
text report prints the same command after each pattern, and run is empty for
a pattern no graph verb fits.

## Options

**--dir** *string* (default: .)
: Repo directory to install into (agent install)

**--dry-run**
: Print what would be written and removed without touching the filesystem (agent install)

**--force**
: Overwrite existing installed skill files (agent install)

**--global**
: Allow absolute destination paths in write mode (agent install)

**--prune**
: Also remove installed skills this binary no longer ships; without it they are reported and left in place, and only skills magus wrote are ever candidates (agent install)

**--skill-form** *string* (default: both)
: Skill form to install: both (default), short, or full (agent install)

**--tar**
: Stream a tar archive to stdout instead of writing files (agent install)

### agent harness apply options

**--id** *string*
: Harness ID; omit to apply every magusfile-wired provider

### agent harness remove options

**--id** *string*
: Harness ID; omit to remove every magusfile-wired provider

### agent harness verify options

**--id** *string*
: Harness ID; omit to verify every magusfile-wired provider

### agent harness install options

**--id** *string*
: Harness ID; omit to install every magusfile-wired provider

### agent adoption options

**--commands** *string*
: File of shell commands, one per line; without it commands are read from stdin

## Subcommands

**install**
: Render the embedded skills and write or stream them into named destinations

**harness**
: Apply, remove, or verify harnesses wired in the magusfile or JSON descriptors

**starter**
: Print a starter AGENTS.md to stdout; never writes a file

**adoption**
: Report how often agents used the knowledge graph versus a raw text search

## Examples

*Install into a repository's agent skills directory*

```sh
magus agent install .agents/skills
```

*Refresh installed skills*

```sh
magus agent install .agents/skills --force
```

*Refresh, and drop skills this version no longer ships*

```sh
magus agent install .agents/skills --force --prune
```

*See what a prune would remove first*

```sh
magus agent install .agents/skills --prune --dry-run
```

*Install anywhere via tar*

```sh
magus agent install --tar | tar -xf - -C .agents/skills
```

*Print a starter AGENTS.md*

```sh
magus agent starter
```

*Measure graph adoption from shell commands*

```sh
magus agent adoption --commands commands.txt
```

*Read commands from stdin*

```sh
magus agent adoption < commands.txt
```

*The report as JSON, for a dashboard*

```sh
magus agent adoption --commands commands.txt -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

