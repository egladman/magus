---
title: "ADR 0006: the person drives review"
order: 6
description: Draft. Agents write code faster than we can review it, and review has become the bottleneck that does not scale. This records what a review of agent-written pull requests found, what magus enforces on its own tree versus what it ships to anyone else, and how a local review reads in an order built from the code's own relationships, with a person driving every step.
tags: [adr, decision, review, agents, guard, diff, conventions, precedents]
status: proposed (a draft of where the thinking stands)
date: 2026-10-07
---

# ADR 0006: the person drives review

Each item below carries its own state: _done_ is in the tree, _in progress_ sits on a
branch, _planned_ is decided and queued, _proposed_ is an idea not yet decided, and _not
built_ was weighed and declined.

## Context

Agents write code far faster than we can review it, and we are now the bottleneck. When you write code you
understand it as you go. When an agent writes it, you meet it for the first time as a
reviewer, a third party to code you did not write, and you start at a disadvantage.

The review process has not caught up, and it does not scale:

- Most of us first read a change in the GitHub UI, after the push, when colleagues or the
  public can already see it. That UI makes a large change hard to navigate.
- Teams answer the volume by shortening review cycles and thinning review, so fewer
  people read each change, faster. That trades review for speed.
- With one approval required, a change often merges while a second reviewer is halfway
  through reading it. Their time is gone, and nobody counts it.

Left to run on their own, agents creep scope and ship slop. A review of this repository's
open pull requests, all agent-written, found exactly that:

- One reverted its own first approach a commit later and kept the test written for it.
- One claimed "five real races" it never named, and its gate change broke the merge queue.
- One widened a security guard with bypasses we found in minutes, and bundled an
  unrelated fix under a changelog file named for a third topic.
- One shipped a 485-line analyzer whose default mode cannot catch the problem that
  justifies it.

An earlier change shipped the Vale prose linter as a built-in spell, which I never
intended, and the port that followed briefly handed this repository's own prose taste to
every user as `magus\prose()`. An independent review measured about 57% false positives
for those rules on the tree they were written for.

Each of these came from a session nobody drove: scope crept, claims went unchecked, and a
standard from one tree leaked into the product.

## Decision

### Principles

1. **A person holds both ends.** I open the work by deciding what it is for and what it
   must not touch, and I close it by reading, questioning and cutting what came back. The
   agent does the middle, often the largest share, but never the start or the finish. I
   drive the session the whole way; an agent never decides the work is done.
2. **People answer people.** A reply to a reviewer is typed by the person who sends it.
   An agent may show a short outline to think with, never text to copy, and it never
   posts a reply. Choosing the words is part of answering for the change.
3. **Write for people.** The moment we start writing code with the intent of other
   agents reading that code instead of other humans is the moment we have lost our way
   as software engineers. Code and comments are for a human reader, and text padded for
   an agent's context window is a defect.
4. **People merge.** An agent never enables auto-merge or queues its own pull request.
   Whether a change met its criteria is my call.
5. **Review before publication.** The first careful read happens locally, before a push
   puts the change in front of anyone else.
6. **Enforce only what we know is right.** magus refuses only what cannot be undone.
   Anything else it ships advises, and anything mined from or tuned to this repository
   stays this repository's policy.

### 1. What magus enforces on itself versus what it ships

| Item                                                                                                                                                                                                                                            | State          |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------- |
| "Conventions" means this repository's policy (`libs/conventions`, `hack/lint`, `hack/policy`); "precedents" means norms mined from a user's own tree. Separate homes; no `conventions/precedents`                                               | done (decided) |
| The root module never imports `libs/conventions` or `libs/testlayout`; a depguard rule enforces it                                                                                                                                              | done (#550)    |
| Vale is not a spell. Its rules run as native Go in `libs/conventions/prose`, judged by a repo-only runner over symbol docs                                                                                                                      | done (#550)    |
| magus ships symbol doc comments as data (`magus\symbols()`), never a judging call; a user's own magusfile decides what text must say                                                                                                            | done (#550)    |
| coldread folds into `libs/conventions` and judges doc comments from the symbol index in every language; its in-body checks are dropped                                                                                                          | done (#550)    |
| A shipped rule must be justified without magus's own data, be a correctness property or documented tool norm, show near-zero false positives on three or more repositories other than magus, and only advise unless the action cannot be undone | done (decided) |
| `fieldwise` stays repo policy with report-partial on, after its false positives are fixed                                                                                                                                                       | done (#554)    |

### 2. The guard denies only what cannot be undone

| Item                                                                                                                                                                                                                                                                          | State                        |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------- |
| Built-in rules that deny a recoverable action (a whole read, a pipe, an in-place edit, staging everything, a raw tool) advise by default, once per session                                                                                                                    | done (#550)                  |
| Every built-in rule can be set by name in the root magusfile: `magus\guard.builtins({name: "deny" \| "advise" \| "off" \| {decision, lines}})`; an unknown name is an error; the stricter of the committed and working-tree settings applies, so loosening waits for a commit | done (#550)                  |
| No number measured on this repository ships in the binary; this repository restores its stricter settings, with the evidence, in its own guard policy                                                                                                                         | done (#550)                  |
| A rule that guards destroyed work, publication, provenance, credentials, the guard's own authority, or a boundary a job declared keeps denying by default                                                                                                                     | done (#550)                  |
| A guard rule that denies an agent enabling auto-merge on its own pull request (`gh pr merge --auto` and equivalents); the denial hands the person the command to run, and it lives in this repository's guard policy                                                          | done (`hack/policy/gh.buzz`) |

### 3. Local review in reading order

Research (empirical, prior art, and what magus already has) is in the plan this ADR came
from. What it established:

- Position steers attention: reviewers comment less on later files and miss more defects
  there. Any order we pick matters, so the central change goes first.
- Grouping related parts helps more than any one reading direction. Studies support
  showing a definition before its use; nobody has tested top-down against bottom-up.
- Reordering cuts noise and false alarms. Nobody has shown it finds more bugs, so we
  promise comprehension and nothing more.
- No shipped tool orders a change by real symbol relationships, locally, before push, the
  same way every time. The closest product runs hosted, after the push, and lets a model
  pick the order.

| Item                                                                                                                                                                                                                                                                   | State    |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- |
| `magus diff` carries a reading order with no flag: hunks grouped by definition and use among the changed symbols, definitions before uses, interfaces before implementations, code before its tests, larger and wider-reaching groups first                            | done     |
| Each step names the relationship that placed it; a completeness line proves every hunk appears exactly once; the order is deterministic and never chosen by a model                                                                                                    | done     |
| Consecutive hunks from one file merge into one step; generated output folds into a group of its own; hunks magus cannot place form a final "unranked" group; when the symbol index cannot be brought current the order is omitted and a note names `magus graph build` | done     |
| The text report, the terminal viewer and `-o json` follow the order, and the console's focus mode walks it one step at a time with everything else put aside                                                                                                           | done     |
| The closing read keeps private read marks keyed by hunk content; `magus diff --unread` lists the hunks no mark covers and always exits 0; `magus diff --print-hook` prints a pre-push hook that runs it, which magus never installs; nothing blocks a push             | done     |
| The reading order opens with the intent: the job's criteria, the brief, and the rationale the session recorded, since magus already holds what an agent was asked and what it ran                                                                                      | proposed |
| A Buzz prototype ordered four recent pull requests; it read better than file order where a change was one connected feature, and worse where it was many small unconnected hunks                                                                                       | done     |
| Prerequisites the prototype found: hunks with their symbols on `magus\diff()`, a file and line to symbols lookup, an index-freshness check, and an `implements` edge from SCIP (knowledge schema 17)                                                                   | done     |

### 4. Review between people

| Item                                                                                                                                                                                                                                                                                           | State          |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------- |
| A reviewer can mark a review as being read now. The mark is local; magus prints the `gh pr comment` line that tells the pull request, and the person runs it. It is a notice, not a hold: it does not stop the merge, and a merge that lands while the mark is set is reported to the reviewer | done (decided) |
| Review time lost to merges that landed during an active review is measured by two opt-in metrics: a count of such merges and the time from the mark to the merge                                                                                                                               | done           |
| A person enables auto-merge; an agent, or tooling acting for one, never does                                                                                                                                                                                                                   | done (decided) |
| A review thread from the forge shows with its replies under the hunk of its first comment, in the terminal and the console, and a reply goes to a named thread                                                                                                                                 | done           |
| The host's resolved state is not fetched. A thread carries only `outdated`, set when its commented line has left the head                                                                                                                                                                      | done (decided) |
| A person can ask magus for a brief on one thread (`magus diff --thread`) and carry it to a model of their choice. It holds the thread, its hunk, and what the graph proves about the symbols changed there; magus sends it nowhere                                                             | done           |
| An agent's outline of a reply is at most five topics of 60 characters that cannot be copied or inserted; the person types every reply                                                                                                                                                          | done           |

### 5. Text a stranger can read

Reviewers read our pull requests and design docs as answers to a question nobody showed
them. The fix is the principle to write for people, enforced.

| Item                                                                                                                                                                                                          | State                   |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----------------------- |
| Pull request text and hand-written Markdown keep to written-prose rules (reply voice, tense, hedges, attribution, filler), judged in Go by one judge that the guard, a CI check and the Markdown lint all run | done (#558)             |
| A pull request description opens with the goal behind the change and why this code stands in its way; an area the diff touches that the text never names draws a question, never a refusal                    | done (#558)             |
| No commit, pull request or page credits a tool or narrates how the work was made                                                                                                                              | done (#558)             |
| Skills stay terse (25 words a sentence and 60 a paragraph in the short form) and say "rule" only for what magus enforces; the rest are instructions                                                           | done (#558)             |
| Every ADR follows one template, with its status and date in front matter; prose uses plain punctuation and no spaced hyphen as a dash                                                                         | done (#555, #557, #558) |

## Alternatives

Weighed and declined:

- An order chosen by a language model. It changes between runs, and it anchors the
  reviewer on the model's account of the change instead of the code it leaves out.
- Review scores, completion ratios, or anything that rewards clearing a review unread.
- A judging host call that ships this repository's prose rules to users (`magus\prose()`).
- One-human-one-agent review as a speed target.

## Consequences

- A workspace that relied on a built-in refusal of a recoverable action gets advice
  instead, and declares the refusal with `magus\guard.builtins` if it wants it back.
- This repository keeps its standards in its own policy files, and a depguard rule stops
  them from reaching the binary. Promoting one to something magus ships means meeting the
  bar in section 1 first.
- The reading order depends on the symbol index: per-hunk symbols, the uses of the changed
  symbols, and an `implements` edge. The edge raised the knowledge schema to 17, so a graph
  built before it rebuilds. Where the index is missing or stale, the order is omitted and a
  note names the rebuild, because an order drawn without the uses would read as "nothing
  is related".
- Marking a review as being read is a notice, and nothing holds a merge. A merge can still
  land under a reader. In exchange magus keeps no state on the forge, and the metrics show
  how often it happens.
- A thread the host calls resolved looks the same as an open one. Fetching that state
  takes a query the comments listing does not make, and `outdated` already says when the
  code moved.

## Open questions

- Whether the reading order keeps working at the scale agents produce (thousands of
  lines), or whether the answer there is smaller changes.
- Whether the completeness line and the folded group are enough to show what the order
  leaves out, so a reviewer reads the code as well as the grouping.
