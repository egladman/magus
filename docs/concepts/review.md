---
title: Review
description: Read a change with magus, draft remarks as you go, and send them to the pull request they belong to. The review's own conversation renders beside the code it is about, in the console and the terminal alike.
tags:
  [
    review,
    diff,
    pull-request,
    merge-request,
    provider,
    threads,
    github,
    enterprise,
    notes,
  ]
---

# Review

## Why this exists

You read your own change before sending it, and that is a different job from reading a diff.
Some of what you notice is for you. The rest is for whoever reviews it next, and that half
has nowhere to go: you are in a terminal, and your colleagues will read it on a web page you
have not opened. So you retype the remark later from memory, or you drop it.

magus already knows the change: which files a target generated, how far each changed symbol
reaches, which hunks you have read. Tell it one more thing, where the change is
**discussed**, and you can finish a self-review with the remarks already sent.

## What it does

- **You see the review's comments beside the code.** They render under the hunk they were
  written about, in the console and in `magus diff`'s viewer.
- **Your own remarks stay drafts until you send them.** You write them as you read, then send
  the set once.
- **You answer a thread from the same surface.**
- **`magus notes capture`** keeps both halves of the conversation as a note in your knowledge
  graph.

Wire no provider and the diff surface behaves exactly as it did before. Most branches have no
pull request open, and that costs you nothing here.

## Wiring a provider

magus knows nothing about any forge. It calls four reserved function names on a spell a
magusfile selected, and reads what comes back:

```buzz
import "spells/github/review" as ghReview;

magus\review.provider(ghReview);
```

| Op               | Answers                                                     |
| ---------------- | ----------------------------------------------------------- |
| `open_review`    | which review this branch has open, or the reason it has none |
| `review_threads` | the remarks already on it                                    |
| `publish_review` | send a batch of drafts as one review                         |
| `reply_review`   | answer one thread                                            |

A spell may implement a **subset**. A host with no comment API can still take a review body,
so a missing op means that provider lacks the capability, not that the spell is broken. See
[Authoring spells](../guides/authoring-spells.md) for the shape of a provider op.

### GitHub, including Enterprise

`spells/github/review` talks to the REST API over plain HTTP with a token; no `gh` binary is
involved. It reads `GITHUB_TOKEN` through whatever [secret provider](secrets.md) the
workspace wired.

The endpoint comes from `GITHUB_API_URL` and defaults to `https://api.github.com`, so GitHub
Enterprise Server needs one variable and nothing else:

```sh
export GITHUB_API_URL=https://github.example.com/api/v3
```

magus **derives** the git remote's host from that endpoint instead of asking you for it
separately. Your installation has one host, and a second setting would only give you a way to
disagree with the first.

This is a second GitHub spell, beside `spells/github/actions`, and the runtime is what splits
them rather than the vendor. Every op in that one checks for the Actions runtime token first,
which suits a cache that exists only on a runner. A review happens on your laptop, where that
token never exists, so folding these ops in there would leave them inert. Import both when
you want both.

## Reading

In the console's Diff surface:

| Key | Does                                       |
| --- | ------------------------------------------ |
| `c` | write a remark on the hunk under the cursor |
| `a` | answer the thread under the cursor          |
| `s` | read the batch of drafts, then send it      |

`s` lists the drafts and asks for a summary before anything leaves. You often change your mind
about the first remark by the time you write the fifth, which is why they wait.

`magus diff`'s terminal viewer renders the same threads under the same hunks. It cannot
publish, and no command carries a `--publish` flag: you send with the batch in front of you or
you do not send.

### Where a remark lands

A colleague anchors a thread to a line of the **review**, which is not the changeset in front
of you. Your working tree moves after they write, and a pull request covers commits a working
diff does not. So each thread lands in one of three places, and magus never drops one:

- on the hunk holding its line;
- under the file heading, when this changeset no longer contains that line;
- listed as **elsewhere** (press `Esc` for the overview), when the file is not in this
  changeset at all.

## What magus will not do

- **An agent cannot publish.** An agent pairing over MCP reads the review's threads and may
  draft a comment into the shared session. You send it. magus stamps authorship from the
  transport a write arrived on rather than from the payload, so nothing can claim to be you.
- **A self-review is always a `COMMENT`.** The API would accept your change approving itself;
  magus does not offer it.
- **A draft with no line never moves to a line magus guessed.** The send box marks those
  before you send, because a remark that arrives against the wrong code costs more than one
  the host refused.

## See also

- [Authoring spells](../guides/authoring-spells.md) - the provider-op shape and every
  contract magus detects by name.
- [Secrets](secrets.md) - how the token reaches the spell without being written down.
- [Knowledge](knowledge.md) - where a captured review conversation lives afterwards.
