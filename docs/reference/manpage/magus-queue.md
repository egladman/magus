---
title: magus queue
generated_from: internal/cli/registry.go
description: List the changes carrying merge intent, plan them into partitions of independent changes, validate speculative candidates, and merge the green ones through the provider.
tags: [cli, magus queue, merge queue, stacked changes, pull requests, ci]
---

# magus-queue

Merge approved changes through a speculative, partitioned merge queue

## Synopsis

**magus** queue \<describe|ls|plan|validate|apply\> [flags]

## Description

A speculative, partitioned merge queue. A queue run has three steps, each with
the rights it needs and no more.

plan reads the changes carrying merge intent (a mergequeue.changes/v1 document on
stdin or --changes, as ls prints it), checks each one's approval at its head,
finds which changes are stacked on which, drops what conflicts with the base on
its own, and splits the rest into partitions whose affected sets are disjoint.
It writes a mergequeue.plan/v1 document.

validate runs the changes' code and needs read access only. Per partition it
builds candidates base+A, base+A+B and so on onto each other, regenerates
generated files on each, runs --gate on them, and writes a mergequeue.verdict/v1
per change to --verdicts the moment that change is decided.

apply holds the write credential and runs no change's code. It rebuilds each
green change's candidate itself and merges it through the provider, with the
change's own merge method, as soon as everything beneath it has merged. Its
\<source\> is the directory validate wrote, or run:\<run\>, the artifacts of a
validation run as the provider names it (github: \<owner\>/\<name\>/runs/\<id\>).

The checkout is the one at the global --root (default: the current directory),
and every relative path resolves against it. The provider is a built-in name
(github) or a Buzz script. Every verb prints JSONL events (mergequeue.event/v1)
on stdout; ls and describe print their document instead. The global --dry-run makes
apply report what would merge and call nothing on the provider.

### queue describe options

**--app** *slug*
: \`slug\` of the app apply writes with (github: a GitHub App, required with a --status-context)

**--app-id** *id*
: \`id\` of the --app integration, for a provider that cannot read the app (github: the App ID under About on a private app's settings page, never its client id); the status is pinned to it

**--base** *branch*
: \`branch\` the queue merges into

**--provider** *provider*
: \`provider\`: a built-in name (github) or a .buzz file

**--remote** *remote* (default: origin)
: Name of the configured \`remote\` changes and the base are fetched from

**--status-context** *string* (default: merge-queue)
: Commit status the queue posts, whose wiring is described; empty describes what the provider supports and reads no setup

**--vcs** *backend* (default: git)
: Version control \`backend\` of the checkout at --root

### queue ls options

**--base** *branch*
: \`branch\` the queue merges into

**--provider** *provider*
: \`provider\`: a built-in name (github) or a .buzz file

**--remote** *remote* (default: origin)
: Name of the configured \`remote\` changes and the base are fetched from

**--vcs** *backend* (default: git)
: Version control \`backend\` of the checkout at --root

### queue plan options

**--changes** *document* (default: -)
: The mergequeue.changes/v1 \`document\`, or - for stdin

**--depth** *int* (default: 3)
: Candidates of one partition that validate at once

**--facts** *command*
: \`command\` and its arguments, run with no shell and the fact asked for appended, answering what a change affects and which files are generated, for a build tool other than magus; without it the magus workspace at --root answers

**--out** *file*
: \`file\` the mergequeue.plan/v1 document is written to

**--parallel** *int*
: Changes admitted at once; 0 is one per CPU

**--provider** *provider*
: \`provider\` approval at each head is checked with

**--remote** *remote* (default: origin)
: Name of the configured \`remote\` changes and the base are fetched from

**--target** *target* (default: ci)
: magus \`target\` the affected set is computed for; not with --facts

**--vcs** *backend* (default: git)
: Version control \`backend\` of the checkout at --root

### queue validate options

**--facts** *command*
: \`command\` and its arguments, run with no shell and the fact asked for appended, answering what a change affects and which files are generated, for a build tool other than magus; without it the magus workspace at --root answers

**--gate** *command*
: \`command\` and its arguments, run with no shell in each candidate's checkout with the change's affected projects appended; exit 0 is green

**--only** *change*
: Validate this one \`change\`; the changes beneath it in its partition are merged under it but not gated

**--parallel** *int*
: Candidates built or gated at once across every partition; 0 is one per CPU

**--plan** *file*
: The mergequeue.plan/v1 \`file\`

**--regenerate** *command*
: \`command\` and its arguments, run with no shell in a candidate with the change's affected projects appended and the generated files to rewrite listed on stdin

**--remote** *remote* (default: origin)
: Name of the configured \`remote\` changes and the base are fetched from

**--remote-cache-read**
: Let hooks read magus's remote cache from the GitHub Actions cache service through a loopback proxy that forwards lookups upstream with the runner's ACTIONS_RUNTIME_TOKEN and refuses every write; hooks get a stand-in token, cache.remote.trusted_keys, and remote writes off. Refused without the runner's credentials or a trusted key

**--scratch-env** *NAME=DIR*
: \`NAME=DIR\` sets NAME to DIR in the candidate's scratch directory for every hook, so the cache it names is the candidate's own; repeatable

**--target** *target* (default: ci)
: magus \`target\` the affected set is computed for; not with --facts

**--vcs** *backend* (default: git)
: Version control \`backend\` of the checkout at --root

**--verdicts** *directory*
: \`directory\` the plan and the verdicts are written to, one entry per change; apply reads it as its \<source\>

### queue apply options

**--app** *slug*
: \`slug\` of the app whose credential the provider writes with (github: a GitHub App, required). apply refuses to start when the base requires --status-context from another integration (MGS3019)

**--base** *branch*
: \`branch\` the queue merges into; a plan naming another is refused (MGS3028), and a run: source must have run on it

**--committer** *string*
: "Name \<email\>" committing each update commit, overriding the provider's committer; with neither, a change needing one waits and apply stops

**--facts** *command*
: \`command\` and its arguments, run with no shell and the fact asked for appended, answering what a change affects and which files are generated, for a build tool other than magus; without it the magus workspace at --root answers

**--interval** *duration* (default: 10s)
: How often \<source\> is read while following it

**--once**
: Apply what \<source\> holds now and stop, rather than following it until it is complete

**--provider** *provider*
: \`provider\`: a built-in name (github) or a .buzz file

**--regenerate** *command*
: The base's own regeneration \`command\` and its arguments, run with no shell and the projects that regenerate them appended as arguments and the generated files to rewrite on stdin, only where the build tool proves the change touches none of its code; elsewhere apply checks the bundle validation left; no credential reaches it

**--remote** *remote* (default: origin)
: Name of the configured \`remote\` changes and the base are fetched from

**--reproduce-gate** *command*
: The \`command\` validate's --gate is given, shown on each kick-back validation decided so its author can run it again; apply never runs it, and never takes it from a verdict

**--reproduce-regenerate** *command*
: The \`command\` validate's --regenerate is given, shown beside --reproduce-gate

**--scratch-env** *NAME=DIR*
: \`NAME=DIR\` sets NAME to DIR in the rebuild's scratch directory for the regeneration, so the cache it names is that rebuild's own; repeatable

**--status-context** *string* (default: merge-queue)
: Commit status the queue posts; branch protection requires it

**--target** *target* (default: ci)
: magus \`target\` the affected set is computed for; not with --facts

**--vcs** *backend* (default: git)
: Version control \`backend\` of the checkout at --root

**--workflow** *definition*
: \`definition\` a run: source must have run, started by an event that runs the base's own copy of it (github: .github/workflows/queue.yaml); required with a run: source, whose uploads are otherwise refused (MGS3027)

## Subcommands

**describe**
: Ask the provider what it supports on a base and what wiring the queue up still takes; prints the steps to run, or a mergequeue.capabilities/v1 document with -o json

**ls**
: Ask the provider for the changes carrying merge intent; prints a mergequeue.changes/v1 document

**plan**
: Check approval, find stacks, drop what conflicts with the base, and partition by affected set

**validate**
: Build and gate a candidate per change, writing each verdict the moment it is decided (read access only)

**apply**
: Rebuild and merge the green verdicts \<source\> holds as they arrive (holds the write credential; runs no change's code)

## Examples

*Print the commands that wire the queue up*

```sh
magus queue describe --provider github --base main
```

*Print the commands that move it onto your own GitHub App*

```sh
magus queue describe --provider github --base main --app acme-magus-queue
```

*The same for a private app, whose App ID only its settings page shows*

```sh
magus queue describe --provider github --base main --app acme-magus-queue --app-id 2034567
```

*List what carries merge intent*

```sh
magus queue ls --provider github --base main > changes.json
```

*Plan it*

```sh
magus queue plan --provider github --out plan.json < changes.json
```

*Validate every candidate*

```sh
magus queue validate --plan plan.json --verdicts verdicts --gate 'magus run ci'
```

*Merge the green ones as they arrive*

```sh
magus queue apply --provider github --base main verdicts
```

*Merge from a validation run's artifacts*

```sh
magus queue apply --provider github --base main --workflow .github/workflows/queue.yaml run:acme/widgets/runs/7
```

*Plan with a provider of your own*

```sh
magus queue plan --provider providers/gitlab.buzz --out plan.json < changes.json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-shell**(1)](magus-shell.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-job**(1)](magus-job.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-broker**(1)](magus-broker.md), [**magus-mcp**(1)](magus-mcp.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-spell**(1)](magus-spell.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

