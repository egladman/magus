---
title: Reviewing your changes
description: Read a changeset in the order that its consequences suggest, price what landing it costs before you push, and keep a bookmark of what you have actually read - from the terminal, your own editor, the console, or a patch someone sent you.
tags:
  [
    diff,
    review,
    read-receipts,
    impact,
    blast-radius,
    tui,
    ack,
    patch,
  ]
---

# Reviewing your changes

`magus diff` reads the working tree's uncommitted changes. It takes no ref: the subject
is always what you have not committed yet.

```sh
magus diff
```

Two things separate it from `git diff`. Declared target outputs are folded away, because
reading a generated file is reading a machine's restatement of a change made somewhere
else - the source edit is the one to read. And what remains is ordered by what it can
break, widest reach first, rather than alphabetically.

Reach needs a symbol index. Without one there is no ranking key at all, and diff says so
at the top and falls back to path order rather than implying an order it did not earn:

```sh
magus graph build
```

## What landing it costs

```sh
magus diff --impact
```

This is the same question `magus affected --impact` answers, asked of a changeset rather
than of a target: which projects rebuild and which were merely edited, who has been
changing them, an estimate of the rebuild drawn from recorded run durations, what the
workspace's advisors say, which human-authored notes anchor a file or symbol you touched,
which `compat(until:)` markers sit in the files you changed, and what the authors asked
magus while writing it.

That last one is the EVIDENCE section, and it is the only one about the reasoning rather
than the result: the graph queries, explains and lookups an agent ran before it wrote,
one line per distinct subject. It appears when the agent worked under a lease and its host
wires `magus agent install`, because a lease is the only identity shared by the process
that observed the question and the write it explains. Without one the section says so, so
a silent record never reads as a change nobody researched.

None of it is a verdict. Nothing is gated on it and the exit code does not change. Each
section says when it could not measure something, so an empty one reads as "nobody
looked" rather than as a clean bill of health.

## Keeping a bookmark of what you have read

The REVIEW section reports the two things a reader cannot work out for themselves: files
that changed AFTER you read them, and files you have never opened, widest blast radius
first.

It is a bookmark, not a score. There is no ratio, and it stays quiet on a small change
nobody has disturbed - a count with a target is a count that gets cleared instead of
satisfied. It is also never shown to a second person: no team view, no aggregate, no
pull-request comment. A read measure someone else can see is a performance metric, and a
performance metric gets gamed rather than met.

Record what you read, wherever you read it. Read the files in vim, in your editor, in a
pager - whatever you already use - then say so:

```sh
magus diff --ack path/to/file.go path/to/other.go
```

With no paths it covers the whole changeset, and `--reason` keeps a note with it for the
next reader of the report. A receipt covers a file at the content it holds NOW, so editing
that file afterwards voids its receipt - which is exactly what makes "changed since you
read it" answerable.

magus never infers a receipt from an editor or a session. A measure satisfied by scrolling
would launder skimming into review, so `--ack` needs a terminal, and agent hosts are denied
it outright.

## Stepping through it in the terminal

At a terminal, `magus diff` opens the viewer - the same annotations, plus navigation and a
way to mark what you have read. Nothing is hidden behind a keypress: the file lines and
their evidence render there exactly as they do in the report.

`]` and `[` walk hunks, `}` and `{` walk files, `v` marks a hunk read, `.` folds the
generated files back in, `esc` returns to the overview, and `q` leaves. Stepping every hunk
of a file earns that file a receipt without a separate `--ack`.

The viewer joins the same session the console's Diff surface and an agent share, so a hunk
marked in one is marked in the others.

It stands aside wherever it cannot draw - no terminal, `-o json`, `--watch`, a patch
argument, `--impact` - and the report prints instead. That is not a refusal and needs no
flag, so a script or an agent is unaffected by the default. To read the report at a
terminal anyway:

```sh
magus diff --no-tui
```

Or make it the standing preference:

```sh
magus config set key=diff.tui,value=false
```

## Reading a patch you did not produce

Anywhere a patch comes from, `-` reads it on stdin and a path reads it from a file:

```sh
gh pr diff 123 | magus diff -
```

Both dialects parse: git's `diff --git a/x b/x` headers, and the bare `--- a/x` / `+++ b/x`
pair that GNU `diff -u` and `patch` speak. A patch magus cannot read is refused rather than
reported as an empty changeset, because "nothing to review" is the one wrong answer that
costs something - you stop looking.

## Staying in git

You do not have to type a magus command to get this. Wire it as git's pager for `diff` and
plain `git diff` renders through magus, with `git --no-pager diff` still giving you the raw
patch. See [Git integration](integrations/git.md) for that, for the other backends,
and for why an external diff tool is the wrong hook.

## Machine-readable

```sh
magus diff -o json
```

Each file carries `read_state`, so a script or a Buzz advisor can branch on what has been
read without parsing the report.
