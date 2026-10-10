---
title: "ADR 0007: one message, two readers"
order: 7
description: magus prints one stream for a person at a terminal and for an agent with a context budget. This records how a message is written once as typed parts, and how one log handler renders it for whichever reader the invocation has.
tags: [adr, decision, output, logging, diagnostics, guard, agents]
status: accepted
date: 2026-10-09
---

# ADR 0007: one message, two readers

Each item below carries its own state: _done_ is in the tree, _in progress_ sits on a
branch, _proposed_ is decided in outline and not built, and _not built_ was weighed and
declined.

## Context

magus writes one stream, and two kinds of reader consume it. A person at a terminal reads
a message once, wants the reason behind it, and needs to see that a long wait is a wait
and not a hang. An agent rereads every line it is given on every later turn, so each
sentence of rationale costs it context for the rest of the session.

Measured on this tree before this decision:

- A raw-tool denial ran seven lines for a one-line problem: the bootstrap was chained to
  `tail`.
- The read-navigation denial printed a map of up to sixty declarations.
- One `generate` run printed MGS4007 sixteen times, once per step.
- MGS7003 wrapping MGS3035 printed two `see:` links and split a sentence between them.
- Lock and upstream waits logged every fifteen seconds.

Cutting the text for everybody fixes the agent and costs the person. Waits that stay
silent for a minute read as a hang at a terminal, and a diagnostic stripped of its reason
sends a person to the docs page for every error.

## Decision

### A message is typed parts, not one string

_Proposed._ Every message magus prints is built from four parts:

- The verdict is one sentence naming the problem.
- The next command, at most one, acts on it.
- The why carries the rationale, as long as it needs to be.
- The ref names where the full text is kept: `magus query output <ref>`, or a docs page.

Each producer carries the parts as fields, never as one joined string:

| producer             | verdict and next          | why                        |
| -------------------- | ------------------------- | -------------------------- |
| `diagnostics.Error`  | `Msg`                     | `Why`, set by `WithWhy`    |
| guard `ShellVerdict` | `Deny`, `Next`            | `Why`                      |
| slog record          | the message               | an `attr.Why(...)` attribute |

A wait or heartbeat record carries `attr.Elapsed(d)`, which is how the handler tells
progress from news.

The `diagmsg` analyzer judges the verdict alone: a length cap, one causal clause, one
command and no leading tag. A `why` is exempt, so a reason never has to be cut to pass
lint.

### The invocation resolves one audience

_Done._ Each invocation resolves `human` or `agent` once, from what magus was told and
never from where it runs ([Told, never guessed](../doctrine.md)). The first signal that
answers wins:

1. `log.audience`, set in magus.yaml, as `--log-audience`, or as `MAGUS_LOG_AUDIENCE`,
   which the harness configuration `magus describe harness` prints sets for an agent
   host. magus names no host in its own source.
2. The guard hook and the MCP server, both agents by construction.
3. A `magus.lease` member in `BAGGAGE`.
4. Nothing told: `human`. A terminal, a pipe and a CI runner read the same output.

### One handler renders for the audience

_Done._ `audience.Wrap` wraps whichever handler `verbosity.go` installs, so each
policy lives in one place instead of at two hundred call sites:

| policy       | human                                  | agent                                         |
| ------------ | -------------------------------------- | --------------------------------------------- |
| `why`        | a dim second line                      | kept in the run log, reached through the ref  |
| waits        | from the first beat, then at each doubling | silent until a minute, then at each doubling |
| repeats      | folded into one footer line with a count | the same                                    |
| decoration   | color and glyphs                       | none                                          |
| component    | a leading `name: `                     | the same                                      |

`-o json` and `-o jsonl` carry every part as fields for both readers, since a parser
selects what it shows. `-v` at the agent audience prints `why` inline: asking for more
is the signal.

### What this branch already does

- _In progress._ Guard denials print the verdict, one command and the ref, and the
  rationale moves into the stored verdict. Only agents receive guard denials, so no
  person loses text.
- _Done._ A nested diagnostic prints one `see:` link and names the inner code inline.
- _Done._ A diagnostic repeated across steps prints once, then one footer line with the
  count.
- _In progress._ slog messages carry a `component` attribute instead of a `name: `
  prefix.
- _Done._ Lock and upstream waits log at their own cadence, each note carrying
  `attr.Elapsed`; only the agent display holds back the notes under a minute.
- _Done._ MGS3035, MGS4007 and MGS7003 keep their short verdicts and carry their reasons
  as `why`: dim under the cause for a person, a `why` field in `-o json`, and in the run
  log for an agent.

## Not built

- **Rewriting prose at render time.** Summarizing a message by model or by pattern is
  nondeterministic, adds latency to every line, and nothing can test it.
- **An `if agent` branch at each call site.** It spreads one policy across every message
  and drifts the first time a site forgets it.
- **A second catalog of agent wording.** Two texts for one message drift apart, and
  nothing reports which one went stale.

## Open questions

- Whether an MCP tool result carries `why` as a separate field or only the ref.
