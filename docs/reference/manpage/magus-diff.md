---
title: magus diff
generated_from: internal/cli/registry.go
description: "Report every uncommitted change annotated with what the workspace knows: whether it is generated, how widely its changed symbols are referenced, whether it is public API surface, and what coverage was observed."
tags: [cli, magus diff, diff, review, changeset, semver]
---

# magus-diff

Read the working tree's changes in the order they deserve attention

## Synopsis

**magus** diff [--generated] [--impact] [--no-tui] [--watch] [--rev \<base\>...\<head\>] [--patch \<file\>|-] [\<path\>...] [flags]

## Description

Read the working tree's uncommitted changes, annotated and ordered.

A changeset is not a list of files, it is a set of CONSEQUENCES, and a reader's
attention is scarce. Alphabetical order spends it at random: it gives a
regenerated lockfile the same weight as a signature change twelve packages
depend on. This orders by what a change can BREAK.

Generated files - declared target outputs - are folded away by default. Reading
one is reading a machine's restatement of a change made somewhere else, so the
source edit is the review. Pass --generated to see them anyway.

At a terminal this opens an interactive viewer: the same annotations, plus
navigation and a way to mark what you have read. It is the same report either
way - the viewer renders the identical annotation lines - so nothing is hidden
behind a keypress, and ] and [ walk hunks while q leaves.

The viewer stands aside on its own wherever it cannot draw: no terminal, -o json,
--watch, a patch argument, or --impact. Those are not refusals, they are the
report printing instead, so a script or an agent needs no flag. --no-tui is for
a person who wants the report at a terminal anyway, and
"magus config set key=diff.tui,value=false" makes that the standing preference.

One rule decides what a word on the command line means: the CHANGESET is always
named by a flag - --rev, --patch, or the working tree by default - and every
positional is a PATH that narrows it. So "magus diff internal/ledger/" reads only
that subtree, and it means the same thing whichever source it is narrowing.

The patch source moved behind --patch for that rule. A bare - still reads stdin,
because it is unambiguous and it is what every pipe in the world already types.

--rev reads a committed range as base...head rather than the working tree, which
is how you review a branch: a colleague's, or the one your own agent just
finished. It is the half a working-tree diff cannot reach, and it keeps the
viewer, the navigation and the marks, because a revision is a tree state magus
can address rather than a patch somebody handed it. Three dots: the answer is
what head added since it diverged, never what the base gained meanwhile.

A receipt earned against a range attests to the blob at that revision, so it
survives the working tree moving underneath and does not follow the branch when
somebody force-pushes it. That is the whole difference from a working-tree
receipt, and it is why --ack takes --rev where it refuses a patch: magus can name
what a range receipt covers, and cannot name what a patch on stdin covers.

Each file carries the evidence behind its rank: how many files reference the
widest changed symbol it defines, whether any of those referents cross a project
boundary or the module boundary (which is the question a version bump turns on),
and the coverage a prior run observed. None of it is a verdict. magus does not
claim a change is breaking - deciding that needs signature compatibility, which
needs a base-side index magus does not keep and language semantics it does not
model - it reports who can see the thing you changed and lets you decide.

The console's Diff surface reads the same annotations over the same session,
and an agent can join that session through the magus_diff MCP tool.

--impact appends the blast radius of landing the change: which projects rebuild
and which were merely edited, who has been changing them, an estimate of the
rebuild from recorded run durations, what the workspace's advisors say, and
which human-authored notes anchor a file or symbol you touched. It is the same
question magus affected --impact answers, asked of a changeset instead of a
target. It is context and never a verdict - nothing is gated on it and the exit
code is unchanged; neither the flag nor the section it prints says "preflight",
because in this workspace's magusfiles a preflight target IS a gate and this
must never read as one. Each section says when it could not measure something,
so an empty one reads as "nobody looked" rather than as a clean bill of health.

--impact also carries a REVIEW section, which is a bookmark rather than a
score. It reports the two things a reader cannot produce without reading:
files that changed AFTER they were read, and files never opened, widest
blast radius first. It reports no ratio and stays silent on a small change
nobody has disturbed - a count with a target is a count that gets cleared
instead of satisfied.

--prompt prints a review prompt for you to paste into whichever model you
use, and magus stops there: it calls no model, holds no key, and sends
nothing. It is the same refusal magus agent makes about your AGENTS.md -
magus generates the text and a person carries it across, because a tool that
crossed the boundary itself would leave bytes you did not write and cannot
audit. It asks for findings rather than review prose; the words your
colleague reads should be yours. Add --impact for the rationale behind each
instruction.

A receipt covers a file at its CURRENT content, so editing it afterwards
voids the receipt. Stepping a file through in the viewer earns one; --ack covers
the changeset at once and takes an optional --reason kept with it. magus
never infers a receipt from an editor or a session: a measure satisfied by
scrolling would launder skimming into review. --ack refuses without a
terminal, and agent hosts are denied it outright.

The count is never shown to anyone but the reader. There is no team view and
no pull-request comment, because a read measure a second person can see is a
performance metric, and a performance metric gets gamed rather than met.

## Options

**--ack**
: Record that you have read the changed files at their current content; --impact reports what carries no such record

**--generated**
: Include declared target outputs, which are folded away by default

**--impact**
: Append the blast radius of landing this: reach, ownership, an estimate from recorded run times, advisors, and note anchors

**--no-tui**
: Print the report instead of opening the interactive viewer

**--patch** *-*
: Review a patch somebody handed you instead of the working tree; \`-\` reads stdin

**--prompt**
: Print a review prompt to paste into your own LLM: the context magus has, never a drafted review. With --impact, also carries the rationale behind each instruction

**--reason** *string*
: An optional note kept with an --ack, for the next reader of the report

**--rev** *string*
: Review a committed range instead of the working tree, as base...head: a colleague's branch, or your agent's finished work

**--watch**
: Re-read and re-render whenever the working tree changes

## Exit status

**0**
: The changeset was read and rendered. This is the status whether or not anything changed: unlike git diff --exit-code, a non-empty changeset is not a failure, and no flag makes it one.

**1**
: The changeset could not be read - an unreadable patch file, or stdin, or a working tree the VCS would not report on.

**2**
: Misuse: more than one patch argument, or an argument that is neither a readable patch nor -. The viewer never causes this: where it cannot draw - no terminal, -o json, --watch, a patch argument, --impact - it stands aside and the report prints instead.

## Examples

*Read what you are about to commit*

```sh
magus diff
```

*Review a branch somebody else pushed*

```sh
magus diff --rev main...feat/audience
```

*Narrow it to one subtree*

```sh
magus diff internal/ledger/
```

*Include the generated files too*

```sh
magus diff --generated
```

*Everything to know before landing it*

```sh
magus diff --impact
```

*Print the report instead of opening the viewer*

```sh
magus diff --no-tui
```

*Build a review prompt for the model of your choice*

```sh
magus diff --prompt
```

*Machine-readable, for a script or a Buzz advisor*

```sh
magus diff -o json
```

## See Also

[**magus**(1)](magus.md), [**magus-ls**(1)](magus-ls.md), [**magus-describe**(1)](magus-describe.md), [**magus-run**(1)](magus-run.md), [**magus-x**(1)](magus-x.md), [**magus-where**(1)](magus-where.md), [**magus-affected**(1)](magus-affected.md), [**magus-graph**(1)](magus-graph.md), [**magus-query**(1)](magus-query.md), [**magus-explain**(1)](magus-explain.md), [**magus-path**(1)](magus-path.md), [**magus-refs**(1)](magus-refs.md), [**magus-watch**(1)](magus-watch.md), [**magus-events**(1)](magus-events.md), [**magus-status**(1)](magus-status.md), [**magus-clean**(1)](magus-clean.md), [**magus-vcs**(1)](magus-vcs.md), [**magus-doctor**(1)](magus-doctor.md), [**magus-config**(1)](magus-config.md), [**magus-session**(1)](magus-session.md), [**magus-memory**(1)](magus-memory.md), [**magus-notes**(1)](magus-notes.md), [**magus-server**(1)](magus-server.md), [**magus-buzz**(1)](magus-buzz.md), [**magus-completion**(1)](magus-completion.md), [**magus-man**(1)](magus-man.md), [**magus-init**(1)](magus-init.md), [**magus-agent**(1)](magus-agent.md), [**magus-self**(1)](magus-self.md), [**magus-version**(1)](magus-version.md)

