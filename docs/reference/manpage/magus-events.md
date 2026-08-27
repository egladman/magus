---
title: magus events
generated_from: internal/cli/registry.go
description: Stream magus events as JSONL - one event per line - so an editor plugin, a status bar, or any other integration can react to runs, results, and diagnostics.
tags: [cli, magus events, events, integration, editor, plugin, jsonl, subscribe]
---

# magus-events

Stream workspace events as JSONL for an integration to consume

## Synopsis

**magus** events [flags]

## Description

Stream workspace events as JSONL, one event per line. This is the surface
third-party integrations build against: an Emacs or Vim plugin, a status bar,
a notifier.

Every magus process in the workspace feeds the stream, so a run started in
another terminal shows up here. It needs no daemon, no token, and no loadable
magusfile - an editor can attach to a repository whose magusfile is mid-edit.

The stream is outbound only. Nothing a subscriber does can change a magus
verdict; the inbound counterpart is the session hook.

target.output is excluded unless named with --type: it is the one event type
that scales with build size rather than project count. To read a target's full
log, take the ref from its target.result event and pass it to query output.

A subscriber must ignore event types and fields it does not recognize. That is
what lets the taxonomy grow without breaking clients built against an earlier
schema.

## Options

**-f**
: Short for --follow

**--follow**
: Keep streaming as events occur instead of exiting after the replay

**--interval** *duration* (default: 250ms)
: How often --follow polls the run log for new events

**--limit** *int* (default: 20)
: Replay this many recent invocations before following; 0 replays nothing (only new events), negative replays every retained run

**--type** *string*
: Restrict to these event types (comma-separated); default is every type except target.output

## Examples

*Watch a workspace live*

```sh
magus events --follow
```

*Only what an editor needs for diagnostics*

```sh
magus events --follow --type target.result,diagnostic.emitted
```

*What did the last run do*

```sh
magus events --limit 1
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-diff**(1)](magus-diff.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

