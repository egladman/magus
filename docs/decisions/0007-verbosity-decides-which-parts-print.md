---
title: "ADR 0007: verbosity decides which parts of a message print"
order: 7
description: A message is written once as typed parts, and the verbosity flags decide which parts print. The default keeps every reason and progress note; -q and -s keep the verdict, the next command and the ref.
tags: [adr, decision, output, logging, diagnostics, guard, verbosity]
status: accepted
date: 2026-10-09
---

# ADR 0007: verbosity decides which parts of a message print

Each item below carries its own state: _done_ is in the tree, _in progress_ sits on a
branch, _proposed_ is decided in outline and not built, and _not built_ was weighed and
declined.

## Context

magus printed every reason it had, every time. Measured on this tree before this
decision:

- A raw-tool denial ran seven lines for a one-line problem: the bootstrap was chained to
  `tail`.
- The read-navigation denial printed a map of up to sixty declarations.
- One `generate` run printed MGS4007 sixteen times, once per step.
- MGS7003 wrapping MGS3035 printed two `see:` links and split a sentence between them.
- Lock and upstream waits logged every fifteen seconds.

Cutting the text for everybody costs the person at a terminal. Waits that stay silent
for a minute read as a hang, and a diagnostic stripped of its reason sends a person to
the docs page for every error. A run that asks for less, with `-q` or `-s`, has no use
for either.

## Decision

### A message is typed parts, not one string

_Done._ Every message magus prints is built from four parts:

- The verdict is one sentence naming the problem.
- The next command, at most one, acts on it.
- The why carries the rationale, as long as it needs to be.
- The ref names where the full text is kept: `magus query output <ref>`, or a docs page.

Each producer carries the parts as fields, never as one joined string:

| producer             | verdict and next | why                          |
| -------------------- | ---------------- | ---------------------------- |
| `diagnostics.Error`  | `Msg`            | `Why`, set by `WithWhy`      |
| guard `ShellVerdict` | `Deny`, `Next`   | `Why`                        |
| slog record          | the message      | an `attr.Why(...)` attribute |

A wait or heartbeat record carries `attr.Elapsed(d)`, which is how the display tells
progress from news.

The `diagmsg` analyzer judges the verdict alone: a length cap, one causal clause, one
command and no leading tag. A `why` is exempt, so a reason never has to be cut to pass
lint.

### The verbosity flags pick the parts

_Done._ No new setting decides this. `-q`, `-s` and `log.silent` (`MAGUS_LOG_SILENT`)
already ask for less, and `quiet.Wrap` wraps the display whenever one of them is on:

| part      | default                                    | `-q` / `-s`                                 |
| --------- | ------------------------------------------ | ------------------------------------------- |
| `why`     | a dim second line                          | in the run log, reached through the ref     |
| waits     | from the first beat, then at each doubling | quiet until a minute, then at each doubling |
| console   | the link, how to open it signed in         | the one command that opens it               |
| repeats   | folded into one footer line with a count   | the same                                    |
| component | the name before the message                | the same                                    |

`-v` brings `why` back under `-q` or `-s`. `-o json` and `-o jsonl` carry every part as
fields, since a parser selects what it shows. The run log keeps everything.

Two commands are quiet without being asked, because no person reads their stderr: the
guard when installed hook glue calls it, and the MCP server. The harness configuration
`magus describe harness` prints sets `MAGUS_LOG_SILENT=true` where the host has a place
for it, which is `-s` on every command run there.

## Alternatives

_Not built._

- **A setting naming who reads the output.** The verbosity flags already say how much a
  run wants, and a second knob would let the two disagree.
- **Rewriting prose at render time.** Summarizing a message by model or by pattern is
  nondeterministic, adds latency to every line, and nothing can test it.
- **A branch at each call site.** It spreads one policy across every message and drifts
  the first time a site forgets it.
- **A second catalog of short wording.** Two texts for one message drift apart, and
  nothing reports which one went stale.

## Consequences

- _Done._ Guard denials print the verdict, one command and the ref, and the rationale
  moves into the stored verdict. A search denial keeps the graph's answer inline.
  Advisories print one line of advice and a ref the same way.
- _Done._ A nested diagnostic prints one `see:` link and names the inner code inline.
- _Done._ A diagnostic repeated across steps prints once, then one footer line with the
  count.
- _Done._ slog messages carry a `component` attribute instead of a name tag in their
  text, and the pretty display still prints the name before the message.
- _Done._ Lock and upstream waits log at their own cadence, each note carrying
  `attr.Elapsed`; a quiet display holds back the notes under a minute.
- _Done._ MGS3035, MGS4007 and MGS7003 keep their short verdicts and carry their reasons
  as `why`: dim under the cause by default, a `why` field in `-o json`, and in the run
  log under `-q` and `-s`.

## Open questions

- Whether an MCP tool result carries `why` as a separate field or only the ref.
