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
| Vale is not a spell. Its rules run as native Go in `libs/proofread`, judged by a repo-only runner over symbol docs                                                                                                                              | done (#550)    |
| magus ships symbol doc comments as data (`magus\symbols()`), never a judging call; a user's own magusfile decides what text must say                                                                                                            | done (#550)    |
| coldread folds into `libs/conventions` and judges doc comments from the symbol index in every language; its in-body checks are dropped                                                                                                          | done (#550)    |
| A shipped rule must be justified without magus's own data, be a correctness property or documented tool norm, show near-zero false positives on three or more repositories other than magus, and only advise unless the action cannot be undone | done (decided) |
| `fieldwise` stays repo policy with report-partial on, after its false positives are fixed                                                                                                                                                       | done (#554)    |

### 2. The guard denies only what cannot be undone

| Item                                                                                                                                                                                                                                                                          | State                        |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ---------------------------- |
| Built-in rules that deny a recoverable action (a whole read, a pipe, an in-place edit, staging everything, a raw tool) advise by default, once per session                                                                                                                    | not built (ADR 0013)         |
| Every built-in rule can be set by name in the root magusfile: `magus\guard.builtins({name: "deny" \| "advise" \| "off" \| {decision, lines}})`; an unknown name is an error; the stricter of the committed and working-tree settings applies, so loosening waits for a commit | done (#550)                  |
| No number measured on this repository ships in the binary; this repository sets read-navigation's line limit, with the evidence for every rule, in its own guard policy                                                                                                       | done (#550, ADR 0013)        |
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

| Item                                                                                                                                                                                                                                                                                                                                                                                                        | State    |
| ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------- |
| `magus diff` carries a reading order with no flag: hunks grouped by definition and use among the changed symbols, definitions before uses, interfaces before implementations, code before its tests, larger and wider-reaching groups first                                                                                                                                                                 | done     |
| Each step names the relationship that placed it; a completeness line proves every hunk appears exactly once; the order is deterministic and never chosen by a model                                                                                                                                                                                                                                         | done     |
| Consecutive hunks from one file merge into one step; generated output folds into a group of its own; hunks magus cannot place form a final "unranked" group; when the symbol index cannot be brought current the order is omitted and a note names `magus graph build`                                                                                                                                      | done     |
| The text report, the terminal viewer and `-o json` follow the order, and the console's focus mode walks it one step at a time with everything else put aside                                                                                                                                                                                                                                                | done     |
| The closing read keeps private read marks keyed by hunk content; `magus diff --unread` narrows the review to the hunks no mark covers under every `-o` and always exits 0; the managed `pre-push` section hands the `check-drift` job the refs git is pushing, and the job counts the unread hunks of exactly those ranges and raises a desktop notice for them; a commit never does; nothing blocks a push | done     |
| The reading order opens with the intent: the job's criteria, the brief, and the rationale the session recorded, since magus already holds what an agent was asked and what it ran                                                                                                                                                                                                                           | proposed |
| A Buzz prototype ordered four recent pull requests; it read better than file order where a change was one connected feature, and worse where it was many small unconnected hunks                                                                                                                                                                                                                            | done     |
| Prerequisites the prototype found: hunks with their symbols on `magus\diff()`, a file and line to symbols lookup, an index-freshness check, and an `implements` edge from SCIP (knowledge schema 17)                                                                                                                                                                                                        | done     |

### 4. Review between people

| Item                                                                                                                                                                                                                                                                                                             | State          |
| ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | -------------- |
| A reviewer can mark a review as being read now. The mark is local; magus prints the `gh pr comment` line that tells the pull request, and the person runs it. It is a notice, not a hold: it does not stop the merge, and a merge that lands while the mark is set is reported to the reviewer                   | done (decided) |
| Review time lost to merges that landed during an active review is measured by two opt-in metrics: a count of such merges and the time from the mark to the merge                                                                                                                                                 | done           |
| A person enables auto-merge; an agent, or tooling acting for one, never does                                                                                                                                                                                                                                     | done (decided) |
| A review thread from the forge shows with its replies under the hunk of its first comment, in the terminal and the console, and a reply goes to a named thread                                                                                                                                                   | done           |
| The host's resolved state is not fetched. A thread carries only `outdated`, set when its commented line has left the head                                                                                                                                                                                        | done (decided) |
| `magus diff --thread <id>` narrows the review to one thread for the person reading it: the viewer opens on its hunk, and printed it is the conversation, the hunk, and what the change there reaches. `-o json`, the console's thread route and the diff MCP tool's read-only `thread` op return the same record | done           |
| A plain `magus diff` report never reaches the network: it names thread ids only from a running server's session, and with no server it lists none and names the command that starts one                                                                                                                          | done (decided) |
| Every command a person types is for that person, and none of its flags exists to feed a model; `magus diff --prompt` and `--print-hook` are removed. The one exception is hook glue: `magus shell`, with `--message` and `--observes-skill-loads`. Agent affordances live on the MCP tools                       | done (decided) |
| An agent's outline of a reply is at most five topics of 60 characters that cannot be copied or inserted, and a second outline of a thread replaces the first; the person types every reply                                                                                                                       | done           |

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

## Amendments

### 2026-10-20: proofread returns a grade with a required minimum

Alternatives above declines "review scores, completion ratios, or anything that rewards
clearing a review unread". That row stays as written. It covers a reviewer's progress through
a change. It does not cover the text an author writes, and proofread now grades that text.
Eli decided this on 2026-10-20.

The forces, from the tree at 002e91670:

- An advise finding fails nothing. The `pr-description` target prints it with
  `magus\log.warn` and passes (`magusfile.buzz:735-758`). That is the warning nobody reads
  in a CI log, which [No silent defaults](../design/judgment/no-silent-defaults.md) rules out.
- The pass bar is a flag, not policy. `--fail-on deny|advise|never` defaults to `never`
  (`libs/proofread/cmd/proofread/main.go:252`), so a bare run exits 0 whatever it finds.
- How far to trust a rule is already measured, then dropped. `evidenceCeiling`
  (`libs/proofread/evidence.go`) records each rule's Wilson 95% lower bound on precision in
  a comment, and uses it only to cap a rule at off or advise. blame is 34/37 (0.79),
  subject-mood 37/40 (0.80), filler 32/40 (0.65), absolute 6/40 (0.07). A finding from
  absolute counts the same as one from subject-mood.

#### Decision

| Item                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                      | State |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ----- |
| A judged text gets a grade: `100 - 100 * sum(w) / (1 + min(words, cap) / k)`, floored at 0. `w` is the rule's Wilson 95% lower bound on precision, pinned in `libs/proofread/evidence.go` and held by a test to what `internal/calibrate` recomputes from the latest labeling round in `testdata/rounds`. A rule no round measured over 30 firings weighs 0. A rule that is off does not fire. `k` is 100 words and `cap` is 1,000, so padding a text past 1,000 words buys no headroom                                                                                                                                   | done  |
| A deny finding fails the text whatever its grade. The grade replaces the advise tier and nothing else                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                                     | done  |
| A grade under its kind's minimum is itself a deny finding, rule `grade` (PRF1100), whose message names the grade, the minimum and the rules that cost the most. Every caller that already fails on deny fails on it, and the findings contract (ADR 0008) gains only a `weight` per finding                                                                                                                                                                                                                                                                                                                               | done  |
| Each kind's minimum sits beside the decisions table in `hack/policy/proofread.buzz`, with its reason, and the decisions file proofread reads carries it. A run over a kind with no minimum exits 2 and names the kind. It never falls back to 0 or 100                                                                                                                                                                                                                                                                                                                                                                    | done  |
| The first minimums are the lowest grade each kind has in this tree at the commit that lands them, so the change fails no text that passes today. Raising one is a commit with its reason. Two kinds depart, each with its reason in `MINIMUMS`: commit-message sits at 20, under its lowest of 74, so the length test below holds; and the review kinds, agent-reply and stylesheet sit at 0, since this repository keeps no such text or this change could not read it                                                                                                                                                   | done  |
| Deleted: `--fail-on` and `reachesBar`; the warn loop in the `pr-description` target; and every `evidenceCeiling` entry a weight replaces. The off entries for rules no round has measured over 30 firings stay, as `unmeasured`                                                                                                                                                                                                                                                                                                                                                                                           | done  |
| Each check's per-kind `advise` list becomes one flag for the whole rule. A rule that advised on any kind is graded on every kind it judges, and its measured weight carries what the list encoded by hand. A rule that advised nowhere keeps denying. Rules that judged some kinds at deny and others at advise (reply-voice, wordy, signpost, leak, changelog-group) lose the deny. This repository puts each back in its decisions table, with a reason, since its own gates relied on them. subject-mood and reply-opener, which their checks declared deny and the ceiling held to advise and off, carry the flag too | done  |
| A test pins the length risk: a commit message of 12 words or fewer, with one finding from the strongest measured rule, grades at or above the commit-message minimum, and two findings grade below it                                                                                                                                                                                                                                                                                                                                                                                                                     | done  |

The exit codes keep their meaning: 0 passes, 1 is a deny finding (a low grade among them),
2 is input proofread cannot judge.

#### Principles checked

- [No silent defaults](../design/judgment/no-silent-defaults.md): a kind with no minimum is an
  error, and advice that changed nothing is gone from CI output.
- [A refusal carries its reason](../design/judgment/a-refusal-carries-its-reason.md): a low grade
  names the minimum and the rules that cost the most, not only a number.
- [Agents propose, humans dispose](../design/judgment/agents-propose-humans-dispose.md): the
  minimums are this repository's policy, committed by a person with a reason. No number
  measured here ships as a default in the binary, as section 2 already requires.
- [The outer harness stays outside](../design/judgment/the-outer-harness-stays-outside.md): the
  guard reads the grade and denies or allows. It rewrites nothing the host sends.

#### Alternatives

- **Keep pass/fail with advise.** It lost because advice fails nothing and a reader cannot
  tell 1 finding from 20. Evidence: the `pr-description` warn loop and `--fail-on` default
  above. It would come back if a grade turned out to track only length (see the next item).
- **A plain count of findings, no weights.** It lost because it treats a 0.07 rule like a
  0.80 rule. Evidence: the bounds in `evidenceCeiling`. It would come back if the weights
  moved between labeling rounds by more than the minimums allow.
- **An uncapped length normalizer.** It lost because padding would buy headroom. It would
  come back if long reference pages hit the cap often and grade unfairly low.
- **A weight for each kind as well as each rule.** This is what the deleted `advise` lists
  approximated by hand. It was not built because no labeling round has measured a rule per
  kind. It would come back with a round whose per-kind lower bounds differ by more than 0.10
  for one rule.
- **A diff-level score.** It stays declined. The 2026-10-10 diff-grade research found that
  size alone separated its classes at AUC 0.82, so a score over a whole change mostly
  measures how big it is.

#### Open questions

- Whether the grade tracks length on short texts too. The diff-grade result (AUC 0.82 from
  size alone) is the reason to doubt it, and the short-commit test pins only one point. The
  first labeling round after this lands should report the correlation between grade and
  word count per kind.
- Whether the Markdown, symbol-doc and help-text lints should grade each text or keep
  reading deny findings alone. They pass deny findings through today, and the `grade`
  finding reaches them now that their kinds have minimums.
- Whether a minimum of 100, which agent-brief, skill-template, cli-help, tool-description
  and review-reply hold, should stay: on those kinds any graded finding fails, as a deny
  would.

### 2026-10-21: the guard denies by default

Section 2 is reversed by [ADR 0013](0013-the-guard-denies.md). Eli decided on 2026-10-21
that advice an agent can route around is routed around: the recoverable rules, and every
advisory whose text names a command, deny by default in the binary. A rule whose remedy
does not clear it is a notice, which never refuses. A workspace still lowers any deny by
name, and loosening still waits for a commit. The section's State rows above are updated
to match; the reasoning stays as written.
