---
title: Reviewing your changes
description: Read a changeset in the order its symbols suggest, a definition before its uses, price what landing it costs before you push, narrow it to the hunks nobody has marked read or to one review thread, and keep a bookmark of what you have read, from the terminal, your own editor, the console, or a patch someone sent you.
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
    reading-order,
    unread,
  ]
---

# Reviewing your changes

`magus diff` reads the working tree's uncommitted changes. To read a committed range
instead, name it with `--rev base...head`.

```sh
magus diff
```

Two things separate it from `git diff`. Declared target outputs are folded away, because
reading a generated file is reading a machine's restatement of a change made somewhere
else, so the source edit is the one to read. And what remains is ordered by what it can
break, widest reach first, rather than alphabetically.

Reach needs a symbol index. Without one there is no ranking key at all, and diff says so
at the top and falls back to path order rather than implying an order it did not earn:

```sh
magus graph build
```

## Reading in order

Below the file list, the report prints the order to read the hunks in. A definition comes
before its uses, an interface before its implementations, and a test after the code it
exercises. Hunks linked that way form a group; larger groups come first, and between equals
the one with the wider reach.

Each hunk carries a sentence saying what placed it:

- `starts the group` opens a group.
- `uses Parse, defined in step 2` follows the hunk that defines `Parse`.
- `defines Parse, used in step 3` precedes the hunk that uses it.
- `implements Store, declared above` and `declares Store, implemented in step 4` pair an
  interface with its implementations.
- `tests Parse, defined above` follows the code the test exercises.

Consecutive hunks from one file share a step. Hunks that use each other share one too.
Generated output sits in a folded group (`--generated` shows it), and a hunk magus cannot
place lands in a final `unranked` group that says why.

The report ends with a completeness line, such as `14 hunks, 14 placed`. It names any hunk
that is missing or repeated, and any changed file with no hunk to show, so you can tell a
whole list from a short one. The same diff and index always give the same order, and no
model chooses it.

The terminal viewer walks the hunks in this order, and the console's focus mode shows it one
step at a time. `-o json` carries it as `order`: `groups` of `steps`, each hunk with its
`why`, and `count`.

It needs the symbol index. When the index cannot be brought current, magus prints no order
and a note says to run `magus graph build`. See [Review](../concepts/review.md#the-reading-order)
for how the order is built.

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

That last one is the EVIDENCE section. Every other section describes the result; this one
describes the reasoning, as the graph queries, explains and lookups an agent ran before it
wrote, one line per distinct subject and no answer text.

It needs two things: a host that wires `magus agent install`, and an agent working under a
lease (`BAGGAGE magus.lease`). The lease is the join, because it is the one identity shared
by the process that saw the question and the write it explains, and it holds across an
orchestrator and the subagents it hands work to.

The record lives in the cache of the tree the work was done in, so a reviewer reading
someone else's branch has none of it. When the list is empty the section names which of the
four silences it is: nothing observed here, observations that carry no lease, leases that
wrote other files, or authors who genuinely asked magus nothing. Only the last is a fact
about the change.

None of it is a verdict. Nothing is gated on it and the exit code does not change. Each
section says when it could not measure something, so an empty one reads as "nobody
looked" rather than as a clean bill of health.

## Keeping a bookmark of what you have read

The REVIEW section reports the two things a reader cannot work out for themselves: files
that changed AFTER you read them, and files you have never opened, widest blast radius
first.

It is a bookmark, not a score. There is no ratio, and it stays quiet on a small change
nobody has disturbed: a count with a target is a count that gets cleared instead of
satisfied. It is also never shown to a second person: no team view, no aggregate, no
pull-request comment. A read measure someone else can see is a performance metric, and a
performance metric gets gamed rather than met.

Record what you read, wherever you read it. Read the files in vim, in your editor, in a
pager, whatever you already use, then say so:

```sh
magus diff --ack path/to/file.go path/to/other.go
```

With no paths it covers the whole changeset, and `--reason` keeps a note with it for the
next reader of the report. A receipt covers a file at the content it holds NOW, so editing
that file afterwards voids its receipt, which is exactly what makes "changed since you
read it" answerable.

magus never infers a receipt from an editor or a session. A measure satisfied by scrolling
would launder skimming into review, so `--ack` needs a terminal, and agent hosts are denied
it outright.

## Before you push

Reading marks are kept by hunk content, so they answer a question the receipts above do not:
which hunks of a range have you not marked read?

```sh
magus diff --unread --rev main...HEAD
```

It narrows the review to the hunks no mark covers: at a terminal the viewer opens on just
those, and printed it is the usual report, filtered. It works under every `-o`. `-o name`
prints one `path:start-end` per unread hunk, ready for a shell loop, and `-o json` is the
usual document, filtered, with an `unread` record. It exits 0 whether or not anything is left,
because a push held up by a read count would make the count the goal. If the marks cannot be
read, it says the read state is unknown and calls no hunk unread.

You do not need a hook of your own to hear about it on a push. The `pre-push` section magus
installs hands off to the `check-drift` job, which adds one line saying how many hunks of the
range being pushed are unread. See [the drift
notice](integrations/git.md#the-drift-notice).

## Telling others you are reading

The console's Diff page has a reading toggle for a review that is open on the host. It
records a local mark, and for a GitHub pull request it shows the `gh pr comment` line that
tells the others. You run that line if you want to; magus never does. The mark is a notice,
and the merge queue ignores it.

If the review merges while the mark is set, magus reports `review.merged` and clears the mark.
Two opt-in telemetry metrics, `magus.review.merged_while_reading` and its `.duration`,
count how often that happens and what it cost. See [Review](../concepts/review.md#saying-you-are-reading).

## One thread at a time

A review thread on the host shows with its replies under the hunk it started on, and the
report names it there as `thread <id>`. The report takes those ids from the running server
and never asks the host itself, so with no server it lists no threads and names
`magus server start`. To read one:

```sh
magus diff --thread 2193847561
```

The id is the thread id, the id of its first comment, or any reply's. At a terminal the viewer
opens on the thread's hunk. With `--no-tui`, or into a pipe, magus prints the conversation, the
hunk, and what the change there reaches: the callers, who it is public to, coverage,
conformance and notes. `-o json` carries the same record. An agent that pairs with you over MCP
may leave an outline of up to five short topics beside the thread; you still type every reply.
See [Review](../concepts/review.md#one-thread) for what the record holds.

## Stepping through it in the terminal

At a terminal, `magus diff` opens the viewer: the same annotations, plus navigation and a
way to mark what you have read. Nothing is hidden behind a keypress: the file lines and
their evidence render there exactly as they do in the report.

`]` and `[` walk hunks, `}` and `{` walk files, `v` marks a hunk read, `.` folds the
generated files back in, `esc` returns to the overview, and `q` leaves. Stepping every hunk
of a file earns that file a receipt without a separate `--ack`.

The viewer joins the same session the console's Diff app and an agent share, so a hunk
marked in one is marked in the others.

It stands aside wherever it cannot draw (no terminal, `-o json`, `--watch`, a patch
argument, `--impact`) and the report prints instead. That is not a refusal and needs no
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
costs something: you stop looking.

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
