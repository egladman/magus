---
title: magus memory
generated_from: internal/cli/registry.go
description: "Manage the per-repository handoff journal that lives outside the checkout: named entries people and agents can read across sessions and worktrees."
tags: [cli, magus memory, handoff, journal, agents]
---

# magus-memory

Durable cross-session project memory

## Synopsis

**magus** memory \<ls|get|put|delete|verify\> [flags]

## Description

Manage the per-repository handoff journal, which is stored outside the
checkout so it survives worktrees and branch switches.

Entries are visible to people and to agents across sessions. They are NOT
automatic model memory: nothing writes one for you, and nothing is recalled
implicitly. An entry earns its place when a later reader needs to reopen the
evidence behind a decision - the run, the query, the output reference, the
document - rather than to be told a conclusion.

An elimination entry records what an investigation ruled OUT: the hypothesis,
why it is dead, and an excerpt of the evidence that killed it. The excerpt is
required because an output reference resolves only from the checkout that
produced it, which leaves the ref beside it a best-effort handle.

verify is the maintenance verb: it reports entries that are malformed, stale,
that link to something no longer there, or whose evidence no longer resolves.
The same entries are reachable through the magus_memory MCP tool and the
console, so a journal written from the CLI is readable by an agent without
either side learning a new format.

### memory put options

**--body** *string*
: Short why/caption, decision, plan and elimination only

**--excerpt** *string*
: The evidence that ruled a hypothesis out, copied inline; elimination only and required there

**--ref** *string*
: Entry ref in 'kind: target' form; repeat for multiple refs

**--reference** *string*
: Name of another entry this one relates to; repeat as needed

**--status** *string*
: Lifecycle label, e.g. accepted, active, done, stale

**--type** *string*
: Entry type: pointer, decision, plan, or elimination

## Subcommands

**ls**
: Show entries and any repair warnings

**get**
: Show one entry

**put**
: Create or replace a named entry

**delete**
: Remove one entry

**verify**
: Check malformed, stale, broken-linked, and unresolvable-evidence entries

## Examples

*List entries and warnings*

```sh
magus memory ls
```

*Read one entry*

```sh
magus memory get release-checklist
```

*Record what an investigation ruled out*

```sh
magus memory put resize-bar-misreported --type elimination --ref 'output: out1a2b3c' --body 'Not the BIOS: the aperture is reported correctly.' --excerpt 'BAR0: 256M ...'
```

*Check the journal's health*

```sh
magus memory verify
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

