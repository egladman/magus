---
title: Doctrine
description: "The standing decisions about what magus automates and what it leaves to your judgment: refusals explain themselves, host wiring stays yours, and disposition stays with a person."
tags: [doctrine, design, judgment, agents, philosophy]
---

# Doctrine

[Scope](scope.md) records what belongs in the tool. This page records when
magus automates a decision and when it hands the decision to you. Each entry
names the mechanism that enforces it and the failure it prevents; check them
against the tool you are running.

## Agents propose, humans dispose

An agent surface can suggest work; it cannot accept it. magus records
authorship from the surface that performed the write, so a change made through
the agent surface carries an agent's name no matter what the writer reports
about itself. Interrupting a person costs attention, and the suggestion
operation reflects that: it requires a stated reason before the proposal
reaches anyone. The delegation ledger has no CLI verb, unlike the attention
events `notify` raises, because an attention event is addressed to a person
while the ledger is an agent-to-agent declaration read back by the guard and
the console.

Automated review is wrong at a steady rate, and wrong in a characteristic way:
the confident finding that "fixes" behavior somebody chose on purpose. The
person deciding whether a proposal lands is the one who catches those findings.
If magus applied them itself, they would ship unexamined.

## The host wiring is yours

magus owns the guard rules and the verdict. It does not own an integration with
your agent host. The rules come from one binary and are identical everywhere;
the part that knows your host - which event field carries the command, which
reply channel reaches the model, what happens when the binary cannot be found -
is a template you copy and edit. Adding a host is your change, not a magus
release.

The contract is small enough to hold in your head. `magus session hook` takes
the thing to judge on stdin, either plain text or a payload keyed by FIELD
(`tool_input.command`, `file_path`, `prompt`), never by a host name or a tool
name. `-o template=` renders the verdict into whatever shape the host's reply
takes. `TestNoHostSpecificBehaviorInCode` fails the build when a host name
reaches a code path rather than a documented path on disk, and the
`magus-guard-coverage:` line each template carries feeds a parity gate that
fails when a host was never asked about a decision the contract grew.

A layer that made every host work with no friction is what is being traded away
here, and it is worth being plain about the trade. Agent hosts change their
event shapes and hook names on their own schedule. A workspace whose
integration lives inside magus waits for a magus release to get unblocked; a
workspace that owns fifteen lines of shell edits them that afternoon. The cost
is charged up front and on purpose: you read your host's hook documentation
once, and there is no zero-configuration path.

The second cost of such a layer is the one nobody sees until it matters. A
guard you did not wire is a guard whose failure mode you do not know, and this
one fails OPEN by design, so a hook that quietly stopped judging looks exactly
like a session with nothing to deny. That is why every shipped template says so
visibly rather than exiting quietly. Whoever runs the guard should be able to
read what it does and repair it without us.

## A refusal carries its reason

A refusal names the mechanism and a next step. Diagnostics are coded and
[documented](reference/diagnostics.md) so you can look one up. A stale binary
can surface as a typo and a missing tool as a lint finding; where magus can
tell the difference, the message says what is happening underneath. The same
rule reaches workflow refusals: `notes edit` declines to rewrite a note's
anchors from a pipe, and the error says why anchors are not pipeable and which
command edits them.

A denial that says no and nothing else sends you around the tool, and the
workaround runs outside the cache and the sandbox, where magus can no longer
account for the work. An error that leaves you with no next step is a doctrine
bug; file it as one.

## Automation you can interrogate

Each automated verdict has a lens that shows its inputs. `affected --explain`
says why a project is in the affected set. `describe target --cache --inputs`
says what hashed into a cache key. `describe file` says whether a file is
generated and by what. `explain` gives a graph node's provenance and edges.
New behavior ships with its lens in the same change: an action magus cannot
account for is a defect in the same class as a wrong answer.

## Manual on purpose

magus could automate each row below, and does not.

| stays manual                | because                                                                                                                                               |
| --------------------------- | ----------------------------------------------------------------------------------------------------------------------------------------------------- |
| disposing an agent request  | an event that means "blocked on input" or "blocked on approval" exists to reach a person; answering it for them removes the person it exists to reach |
| applying suggested changes  | a suggestion lands only when a person accepts it                                                                                                      |
| writing the knowledge store | notes are human-authored by construction: there is no author field to spoof, because authorship rides version control                                 |
| sending a review            | a remark reaches a colleague under your name; an agent drafts into the session and a person sends the batch                                           |

All four would be cheap to build. Each removes the person at the point where
the mechanism needs their judgment, so each stays out.

[Sending a review](concepts/review.md) is the newest of them and the one with
the most surface to give away, so its refusals are worth naming: no command
carries a `--publish` flag, a self-review is always a comment rather than an
approval, and authorship is stamped from the transport a write arrived on
rather than from what the writer claims. A batch waits for a person because
publishing is one outward-facing act; splitting it into a call per remark would
turn the act that needs confirming into a series of small ones nobody confirms.

## Where this is strained

Friction placed wrong is bureaucracy. A refusal that teaches and one that nags
differ in nothing but their message text, and message text rots like any other
prose; the doctrine holds while error messages get the same care as code.

The explain lenses cover verdicts magus computes. magus captures and replays
the output of the tools a target drives, but it cannot make a third-party
tool's reasoning inspectable; the lens stops at the tool boundary.

The host boundary is not as clean as the entry above states it. The binary
still knows a handful of host paths: `magus doctor` inventories the config
locations it can name when it reports on guard wiring, and `agent install`
probes the conventional skill directories. Both are read-only conveniences over
locations on disk, and a host neither one names still works, but extending
either list is a magus release. The envelope field names came from one host's
payload shape rather than from a neutral design, so a host that spells them
differently reshapes its payload before piping it. And the OpenCode plugin
carries a type-check and tests because it is real TypeScript, which is more
upkeep than a shell template and more than an example should need.

The line between automated and manual moves as a mechanism earns confidence.
To move a row out of the table above, edit this page in the same commit that
changes the behavior.
