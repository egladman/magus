---
title: Doctrine
description: "The standing decisions about what magus automates and what it leaves to your judgment: refusals explain themselves, and disposition stays with a person."
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
reaches anyone.

Automated review is wrong at a steady rate, and wrong in a characteristic way:
the confident finding that "fixes" behavior somebody chose on purpose. The
person deciding whether a proposal lands is the one who catches those findings.
If magus applied them itself, they would ship unexamined.

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

| stays manual                | because                                                                                                      |
| --------------------------- | ------------------------------------------------------------------------------------------------------------ |
| disposing an agent request  | an event that means "blocked on input" or "blocked on approval" exists to reach a person; answering it for them removes the person it exists to reach |
| applying suggested changes  | a suggestion lands only when a person accepts it                                                             |
| writing the knowledge store | notes are human-authored by construction: there is no author field to spoof, because authorship rides version control |

All three would be cheap to build. Each removes the person at the point where
the mechanism needs their judgment, so each stays out.

## Where this is strained

Friction placed wrong is bureaucracy. A refusal that teaches and one that nags
differ in nothing but their message text, and message text rots like any other
prose; the doctrine holds while error messages get the same care as code.

The explain lenses cover verdicts magus computes. magus captures and replays
the output of the tools a target drives, but it cannot make a third-party
tool's reasoning inspectable; the lens stops at the tool boundary.

The line between automated and manual moves as a mechanism earns confidence.
To move a row out of the table above, edit this page in the same commit that
changes the behavior.
