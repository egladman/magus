---
title: "ADR 0008: writing a teammate reads"
order: 8
description: Pull request descriptions, review replies and commit messages are read by people who were not there, and text loses tone on the way. This records the evidence, the shape a description takes (outcome first, then how the work came up, why it mattered, the changes), the claims a description may make, the tone rules, the port of the AI-writing checks into the Go prose judge, and the boundaries that keep it usable outside magus.
tags: [adr, decision, writing, conventions, prose, pull-requests, review]
status: accepted (in progress)
date: 2026-10-10
---

# ADR 0008: writing a teammate reads

Each item below carries its own state: _done_ is in the tree, _in progress_ sits on a
branch, _planned_ is decided and queued, _proposed_ is an idea not yet decided, and _not
built_ was weighed and declined.

## Context

A pull request description is read by a reviewer now and by whoever follows a merge
commit to it later. Neither was there when the work happened. Text carries far less tone
than its writer hears in it: Kruger and Epley (2005), as their abstract and later
summaries report it, found readers identified the tone of an email correctly 50 to 56
percent of the time, while senders predicted 78 percent. Byron (2008) found that work email reads as more negative than its sender
meant. Terse, objective text is read as cold, and a reader fills the gap with intent the
writer never had.

What the tree shows, measured over all 573 pull requests on 2026-10-10:

- 386 of 494 non-empty descriptions (78 percent) open with a defect. Among the 17 since
  #558 landed it is 88 percent. Five open with what the change makes possible.
- The judge's own instruction steers it: `lead-context` asks for "the goal behind the
  change and why this code stands in its way", and the guard's `fixLine` repeats it at
  every refusal. Agents see only the refusal text, so the wording of a denial is the
  style guide.
- The cold reading comes from structure, not insults. No description says "naive",
  "hack" or "bogus". What reads as a verdict: absolutes about the past ("has never
  fired", 149 hits of "never"), a pull request as the subject of a fault ("#341 added
  ... without regenerating"), a tool given intent ("magus guessed"), and almost no
  credit to the earlier design (one description in the 141 since #435).
- The 150-word cap of the earlier user-level hook deleted origin sentences to fit, and
  the attribution check refused product names that were the subject of the change ("the
  claude-code spell", the Claude Code mod), forcing vaguer rewrites.
- `reply-voice` refuses every heading, and the shape advice counts every paragraph past
  the lead as a wall, so a description cannot carry a "how we got here" section today.
- The guard and CI disagree: CI's `pr-description` runs only the judge, so an em dash, a
  selling word ("powerful") or an over-budget description passes CI and fails the guard.
  The selling-word list exists twice (Go `writtenFillerWords`, Buzz `puffery`), and so
  does the attribution pattern set.

What review between people adds, from the sources in the research and from patterns seen
in reviews generally:

- A series of pull requests built bottom-up (baseline first, payoff later) reads as cost
  without payoff when the destination is not stated first.
- Evidence shown late, once a thread has stalled, reads as winning an argument rather
  than informing.
- "I do not understand this code" often means "I do not understand why this approach";
  a description that names the alternatives answers it before it is asked.
- A long back-and-forth that one side experiences as debate reads to an onlooker as a
  stalemate. A call settles it faster than a fifth reply.
- Confidence reads from evidence and scope; arrogance reads from certainty words the
  evidence does not carry ("obviously", "as I said").

The same rules are meant for repositories other than this one, written with teammates,
so nothing in the judge may assume magus, git or GitHub.

## Decision

### Principles

- **Static analysis for writing.** These rules are to prose what a linter is to code:
  specific checks with measured precision, deterministic, and usable in any repository
  that adopts them. They hold work written with AI assistance to the standard a careful
  person holds their own work to, and they teach an agent to write that way. They do not
  hide how the work was made: a team that discloses AI assistance does so in its
  contributing guide or a label, which the judge never reads.

- **Lead with the outcome.** The first sentence says what a reader, a user or a
  maintainer can now do, or no longer has to do. The defect appears as the reason, never
  as the subject.
- **Say how the work came up.** A failing run, a measurement, a review, a follow-up to
  an earlier change, a tangent met on the way. This is history of the project, which is
  allowed; who or what produced the change is not.
- **Describe the situation, never the person or the earlier code's character.** "The
  cache kept the old key after a rename", not "the cache was broken". Where the earlier
  design's reason is known, say it: "a per-process cache suited one worker; it stops
  holding at eight".
- **A claim covers exactly what was measured.** Evidence gets a plain statement; a
  judgment call is labeled as one with its reason; what is not known is said.
- **Deterministic checks belong to the Go judge, not to a skill.** A check a regex or a
  parse can prove costs no tokens and cannot drift between two copies. A skill keeps only
  what needs meaning.
- **One judge, no repository knowledge.** The judge reads text and a kind and returns
  findings. Forges, version control and this repository's budget are inputs from the
  layer that calls it.

### 1. The shape of a description

| Part              | Required                                   | What it holds                                                                                                                                                             |
| ----------------- | ------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Lead (no heading) | yes                                        | First sentence: the outcome. Then, in one or two sentences, how the work came up and why it mattered. A change in a series names the destination and its own place in it. |
| What changes      | yes                                        | One bullet per change, present tense, the code as subject.                                                                                                                |
| Why this approach | when an alternative was weighed            | The alternative and the reason it was set aside.                                                                                                                          |
| Evidence          | when the description claims a measurement  | The numbers, how they were taken, and where the run is.                                                                                                                   |
| How we got here   | when the origin needs more than a sentence | The earlier design, what it was for, and what changed around it.                                                                                                          |
| Not verified      | when something was not tested              | What was not checked, and why.                                                                                                                                            |
| Reading guide     | optional                                   | Where to start reading; an offer to walk through it.                                                                                                                      |

Headings are allowed after the lead, in sentence case, phrased as the reader's question
or the section's subject. The stock labels a reply generator emits (`Summary`, `Test
plan`, `Overview`, `Changes`) and bold-label bullets stay refused, because they are the
shape of an answer to a prompt the reader never saw.

The lead is exempt from the word budget. The budget, `60 + 50 * U`, applies to
everything after the lead, so a cut never removes the origin or the reason.

### 2. Decisions, the guard's vocabulary

The judge enforces nothing of its own. Each rule ships a default decision, `deny` or
`advise`, the two words a guard rule's default takes, and a workspace sets any rule to
`off`, `advise` or `deny` the way `magus\guard.builtins` overrides a built-in guard rule.
A caller denies on `deny`, tells `advise` once, and never sees `off`.

- **What ships is portable.** House style, the rules that encode one repository's
  conventions rather than writing a teammate reads (its glossary, plain-ASCII typography,
  no author in a description, the doc-comment budgets, the agent-instruction budgets),
  ships `off`. Run with no table, the judge holds text to tone, claims, shape and the
  generated-writing tells alone.
- **A repository states its own.** This repository turns its house style on in
  `hack/policy/proofread.buzz`, a decisions table beside `hack/policy/builtins.buzz` and kept
  the same way: a reason beside every entry. A path-scoped entry (`blog/**`: a post keeps
  its author's voice) is an exemption with its reason, reported when it no longer matches.
- **No profiles.** A named profile would be a second spelling of a decisions table. A
  voice file is not one: it describes one author's measured style and sets no rule's
  decision, except that it holds `tense` to advice on that author's change descriptions.
- **Every rule is catalogued** like a guard rule, as `{name, code, decision, catches, why}`
  (`proofread rules`), and the docs render a page per rule from it. Each rule also has
  a code in its own `PRF` prefix through `libs/diagnostics`, the shared framework magus
  uses for `MGS` codes and gopherbuzz for `BZZ`, so a finding links to its page.
- **The findings are a contract.** A finding is `{source, kind, rule, code, decision,
  message, match, url}`. Any command that writes the same array, a team's own style
  checker or a reviewer it drives, feeds the same guard rule and lint, and the same
  decisions table applies to it. magus ships one judge; it does not have to be the only
  one.
- **The guard rule names itself.** `pull-request-text` answers as
  `workspace:pull-request-text` rather than as one anonymous workspace rule, so a repeat
  shortens, its verdict is stored and the trail can tell it apart. Every rule in
  `hack/policy` can do the same.

A small change in this repository, before and after (#570):

```text
Before:
The server's warm caches lagged behind edits. A query after a graph-relevant change
rebuilt the graph inline. Symbol indexes that went stale while no server ran stayed
stale until their project was edited again. An edit during a reindex failed it with
MGS4007, and the indexer backed off for minutes.

After:
The first query after an edit answers from a graph and symbol indexes that are already
current. Until now the graph rebuilt inline on that query, an index left stale while no
server ran stayed stale until its project changed again, and an edit during a reindex
backed the indexer off for minutes.

- The warm graph builds when watching starts and rebuilds in the background 500ms after
  the last invalidation.
- ...
```

The same change written for a team adds sections only where they carry something. Angle brackets mark what #570 does not record and its author fills in:

```text
Queries to a running magus server answer from a current graph, so the first question
after an edit no longer waits on a rebuild. This came up while <what surfaced it>:
<what was observed>.

## What changes
- ...

## Evidence
- A real root reindex takes 16s, plus up to 11s for the guard index (<command>, <run>).

## Not verified
- <what was not exercised, and why>
```

### 3. Claims and evidence

A sentence that states a measurement, a comparison or a completion is a claim, and a
claim carries its evidence in the same bullet or sentence.

| Claim kind              | Recognized by                                               | Evidence the judge accepts                                             |
| ----------------------- | ----------------------------------------------------------- | ---------------------------------------------------------------------- |
| Measurement             | a number with a unit, a percentage, `N of M`, `Nx`          | a code span naming a command, test or benchmark; a link; an output ref |
| Comparison              | faster, slower, smaller, fewer, cheaper, more reliable      | same                                                                   |
| Completion              | fixes, eliminates, prevents, guarantees, no longer flakes   | a named test, a linked issue or run, an output ref                     |
| Absolute about behavior | always, never, every, none, all (outside a stated contract) | a named test pinning it                                                |

The `hedge` rule changes with it. A softener inside a claim stays refused ("may help",
"should fix"), because it lets a claim stand with no evidence. A `Not verified` section,
or a sentence that opens with "Not measured", "Not tested" or "Untested", states a limit
and is allowed. A writer who is unsure scopes the claim instead of inflating or
softening it.

In this repository the guard resolves every output ref a description cites (`magus
query output <ref>`): a ref that does not exist, or names a failed run, is refused.
Elsewhere the check is the presence of evidence, not its truth; the skill carries the
rest (section 7). The examples are illustrative, not measurements of this tree:

```text
Refused: Fixes the flaky cache test.
Allowed: `TestCacheEvict` passed 50 of 50 runs with `-count=50`; it failed 7 of 50 before.
Refused: Makes startup much faster.
Allowed: Cold `magus ls` drops from 410ms to 260ms (5 runs each, `hack/bench/startup.buzz`).
Allowed: Not measured on Linux; CI's runners report it.
```

### 4. Tone rules

Each rule names the posture it catches and how to say the same fact instead. Code spans,
quotes, link text and fenced blocks are never judged.

| Rule                                                       | Default                          | Catches                                                                                                                                                                                                                                                      | Instead                                                            |
| ---------------------------------------------------------- | -------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ------------------------------------------------------------------ |
| `condescension` (widened from guides to all written kinds) | deny                             | obviously, of course, everyone knows, needless to say, clearly (as a booster), simply, easy                                                                                                                                                                  | state the step or fact                                             |
| `blame` (new)                                              | deny                             | a person or past work as the subject of a fault: "should have", "failed to", "forgot to", "neglected to" with a person or a pull request as subject; "whoever wrote"; lazy, sloppy, careless, naive, incompetent, stupid, crazy, insane of code or decisions | describe what happened: "the rename left the old key"              |
| `verdict` (new)                                            | advise                           | judgment words about the earlier design: broken (outside "broken test/build/link"), wrong, bad, messy, hacky, ugly, terrible, nightmare, mess, garbage, ridiculous                                                                                           | name the behavior the word stands for                              |
| `absolute` (new)                                           | advise                           | never, always, nothing, nobody, every time about the past or about people ("has never fired", "nobody checked")                                                                                                                                              | say when and how often: "fired 0 times in <N> runs since <change>" |
| `intent` (new)                                             | advise                           | a tool or a person given motives: "pretends", "lies", "guessed", "doesn't care", "hates", "refuses to understand"                                                                                                                                            | describe the mechanism                                             |
| `credit` (new)                                             | advise, change descriptions only | a description that replaces or removes a design and names nothing it did well                                                                                                                                                                                | one clause on what the earlier design was for                      |

The default follows measured precision. A list with no legitimate sense in this tree
denies; a list whose words state invariants elsewhere ("never returns nil") advises,
once per pull request, because the repository's own docs use "never" 788 times
and every sampled use states a contract.

### 5. Review replies

A new kind, `review-reply`, covers a review comment, a review body and a reply in a
thread. It takes every tone rule, plus:

| Rule               | Default | Catches                                                                                                                                                                |
| ------------------ | ------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `reply-opener`     | deny    | a reply that opens by contradicting: "No,", "Actually,", "Wrong", "As I said", "Again,", "Like I said"                                                                 |
| `judgment-as-fact` | advise  | a recommendation stated as a fact with no reason: "This should be a map." Instead: "I'd use a map here: lookups dominate. Open to keeping the slice if order matters." |
| `stacked-hedge`    | advise  | two or more softeners in one sentence, or an apology before a valid point: "Sorry if this is dumb, but maybe...". Instead: state it.                                   |
| `long-thread`      | advise  | the reply is the fourth or later by one author in a thread: suggest a call. Needs the thread length, which the caller passes in.                                       |

The same judge runs on replies a person types (`proofread review-reply` reads a draft on
stdin), and the guard runs it on replies an agent posts. Illustrative replies:

```text
Reads as arrogant: Obviously this needs a lock. As I said, the map is shared.
Reads as unsure:   Sorry, I might be wrong, but maybe this could possibly need a lock?
Reads as expertise: This needs a lock: the map is written from two goroutines
                    (`go test -race` output below).
Judgment, labeled: I'd move the retry into the client: the upstream drops about 1% of
                    calls. Open to keeping it here if you see a cleaner seam.
```

### 6. The AI-writing checks move into the judge

The deterministic parts of the `stop-slop` and `humanizer` skills and of Wikipedia's
"Signs of AI writing" move into `libs/conventions/proofread`. Of 96 pattern families, 52 are
word or phrase lists, 20 are structural, 9 heuristic and 15 need meaning. The proposed
rules catch 33 of the skills' own 41 "before" examples. Each was measured over 261 docs
pages, 665 changelog fragments, 200 merged pull requests and the Go comments; every
error-tier rule has zero hits in the docs pages, changelog fragments and pull requests
except four `wordy` sites in the docs, which the change fixes.

| Rule                        | Catches                                                                                                                                | Default                                                            |
| --------------------------- | -------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------ |
| `leak`                      | chatbot residue: `oaicite`, `turn0search0`, `[cite: 1]`, `grok_card`, unfilled placeholders (`[insert ...]`, `TBD`)                    | deny                                                               |
| `chatbot`                   | "I hope this helps", "feel free to", "great question", "as an AI", knowledge-cutoff disclaimers                                        | deny                                                               |
| `signpost`                  | sentence-initial "Here's the thing", "It turns out", "Let's dive in", "The truth is"                                                   | deny                                                               |
| `buzzword`, `buzzword-weak` | delve, tapestry, testament, pivotal, meticulous, "plays a key role", "serves as a testament"; a weaker list of words with plain senses | deny; advise                                                       |
| `vague`                     | "experts say", "it is widely believed", "paves the way", "only the beginning"                                                          | deny                                                               |
| `closer`                    | "In summary,", "In conclusion,", "All in all,"                                                                                         | deny                                                               |
| `contrast`                  | "not only X but also Y", "it is not X, it is Y"                                                                                        | deny in a change description, advise in docs (15 deliberate sites) |
| `staccato`                  | three or more sentences of six words or fewer in a row                                                                                 | deny in a change description, advise elsewhere                     |
| `ing-tail`                  | ", highlighting ...", ", underscoring ...", ", reflecting ..."                                                                         | advise                                                             |
| `dash`, `ascii`             | em and en dashes, `--`, curly quotes, ellipsis, emoji; ported from Buzz so the Go judge alone reproduces the policy                    | house style: off unless a repository turns it on                   |
| `heading-case`              | Title Case Headings                                                                                                                    | advise                                                             |
| `filler`, `hedge`, `wordy`  | extended lists (truly, fundamentally, "at its core", "in order to")                                                                    | deny                                                               |

Rejected on measurement, because this tree uses them correctly hundreds of times:
Wh-sentence openers, passive voice, three-item lists, "-ly" adverbs, bold density and
every/always/never as a general ban. Those stay in the skill's judgment list.

What remains a skill, about 150 words: run the judge first; then read for a claim with
no specific behind it, false agency, a pull-quote sentence, synonym cycling and padding
triads; keep the author's voice; one tell proves nothing. The phrase tables leave both
skills once the judge carries them.

### 7. Where each piece lives

| Piece                                                                                                                  | Home                                                                                                                                        | Why there                                                                                                                 |
| ---------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------- |
| Rules, kinds, default decisions, `PRF` codes, the catalog                                                              | `libs/conventions/proofread` (Go)                                                                                                           | text in, findings out; no forge, VCS or repository knowledge; its own `go.mod`, installable anywhere                      |
| `proofread <kind> -decisions -only -thread-length`, `proofread rules`, `proofread explain`                             | `libs/conventions/cmd/proofread`                                                                                                            | the one command every layer runs; its findings JSON is the contract an outside judge also writes                          |
| This repository's decisions table and path exemptions                                                                  | `hack/policy/proofread.buzz`                                                                                                                | a repository's own preferences, kept like `hack/policy/builtins.buzz`                                                     |
| A workspace guard rule's name                                                                                          | `types.GuardVerdict`, `magus\guard.deny(reason, {rule})`                                                                                    | a named rule shortens on repeat and is stored, as a built-in is                                                           |
| Which command writes which kind (`gh pr create` writes a description, `gh pr comment` a reply, `glab mr note` a reply) | a table in `hack/policy/` (Buzz)                                                                                                            | recognizing a forge's CLI is forge knowledge, and provider I/O is Buzz (docs/doctrine.md)                                 |
| Thread length for `long-thread`                                                                                        | the forge spells (`spells/github/review`, `spells/gitlab`)                                                                                  | reading a thread is provider I/O                                                                                          |
| Changed lines and files for the budget                                                                                 | a diff-stat method on the backend interface in `vcs/`, exposed through `std/vcs`                                                            | today `branchDiff` passes git's `diff --numstat` through `vcs\cmd`, which fails quietly on Mercurial, Sapling and Jujutsu |
| Output-ref verification, word budget, changelog-fragment count                                                         | `hack/policy/pull_requests.buzz`                                                                                                            | this repository's policy, inputs the judge never sees                                                                     |
| Outside magus                                                                                                          | `proofread` plus a user-level `idiomatic-pr-descriptions` skill; the `idiomatic-hooks pr` hook calls `proofread` instead of its own regexes | one rule set at work and here                                                                                             |

Adding a forge is one table row. Adding a VCS is one more method implementation in
`vcs/`. Adding a check is one Go rule with tests, which the guard and CI pick up because
both already run the same judge.

### 8. What changes together

| Unit                                                                                                                                                                                                                | Write set                                                                                                                               | State                                            |
| ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------- | ------------------------------------------------ |
| U1 judge: the review-reply kind, shape and heading rules, the ported AI-writing rules, the tone rules, the claim rule, one selling-word list                                                                        | `libs/conventions/proofread/*`, `libs/conventions/cmd/proofread/*`, `libs/conventions/readme.md`                                        | done                                             |
| U1b judge speaks decisions: off/advise/deny, house style off by default, `PRF` codes, the catalog, purpose-named kinds, the findings contract                                                                       | the same                                                                                                                                | done                                             |
| U1c the judge is named `proofread`, with a subcommand per kind plus `rules` and `explain`                                                                                                                           | `libs/conventions/proofread/*`, `libs/conventions/cmd/proofread/*`, every caller                                                        | done                                             |
| U2 diff stat for every backend                                                                                                                                                                                      | `vcs/*.go`, `types/vcs.go`, `std/vcs.go`                                                                                                | done                                             |
| U2b a workspace guard rule names itself, and repeats only an identical reason in short                                                                                                                              | `types/guard.go`, `internal/interp/bindings/guard_rule.go`, `internal/guard/workspace_rule.go`, `internal/guard/denial.go`              | done                                             |
| U3 guard and CI: this repository's decisions table, the command table, replies, outside judges, lead exempt from the budget, denials that teach the shape with one example, CI running the same checks as the guard | `hack/policy/*.buzz`, `hack/lint/markdown-proofread.buzz`, `magusfile.buzz` (`pr-title`, `pr-description`), `.github/workflows/pr.yaml` | done                                             |
| U4 docs: writing rules, a page per proofread rule rendered from the catalog, CONTRIBUTING's PR checks                                                                                                               | `docs/conventions.md`, `hack/magusfile/ruledocs.buzz`, `docs/reference/proofread/`, `CONTRIBUTING.md`, `changes/unreleased/`            | done                                             |
| U5 outside this tree: the portable skill and the hook that runs `proofread`                                                                                                                                         | `~/.claude/skills/`, `~/.dotfiles/claude/.claude/hooks/idiomatic-hooks`                                                                 | done (the hook binary installs once this merges) |
| U6 `proofread` ships as a signed release archive beside magus                                                                                                                                                       | `magusfile.buzz`, `hack/magusfile/releases.buzz`, `.github/workflows/release.yaml`                                                      | done                                             |

U3 depends on U1b, U2 and U2b; U4 on U1b's catalog.

### 9. Settled on review

- The judge speaks the guard's decisions (`off`, `advise`, `deny`), not a severity of its
  own, and a repository states its preferences as a decisions table, not a profile.
- Rules are named like guard rules and also carry a `PRF` code, minted through
  `libs/diagnostics` in a prefix of their own; no `MGS` code is spent on a writing rule.
- `verdict`, `absolute`, `intent` and `credit` start at `advise`. Each moves to `deny`
  only after it has fired on real text and every firing was right.
- The guard judges every reply an agent posts: `gh pr comment`, `gh pr review` and
  review comments posted through `gh api`.
- `idiomatic-hooks pr` calls `proofread` and fails closed without it, so the hook and
  the judge cannot drift apart. It keeps no regexes of its own.
- `long-thread` reads its count from `-thread-length N`, and stays silent without it. The
  guard passes none: reading a thread is provider I/O a hook cannot afford. A person
  passes it, or a review flow that has already read the thread through a forge spell.

### 10. Measuring it

| What                                          | How                                                                                                                                                                                                                    | State   |
| --------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | ------- |
| Precision of each rule on this tree           | the judge over the hand-written docs and the last 200 merged pull requests; a `deny` rule with a false positive is narrowed or moved to `advise`                                                                       | done    |
| Whether the rules change what an agent writes | an arm in `benchmarks/agent` that runs SWE-bench tasks with and without the judge in the loop, several runs per task, scoring each description by its deny and advise findings and by a blinded read against section 1 | planned |
| Precision outside magus                       | the shipped defaults over pull requests from repositories that adopt them, before any `advise` rule moves to `deny`                                                                                                    | planned |

## Alternatives

- **Keep the problem-first lead and soften the words.** Rejected: the coldness the
  research found comes from the order and the subject of the sentences, not from any word
  a list could remove.
- **Leave tone to a skill.** Rejected: a skill spends tokens on every description,
  drifts from the guard, and cannot run on a reply a person types. The judgment that
  remains is small enough to state in 150 words.
- **Ban absolutes everywhere.** Rejected on measurement: "never" states 788 contracts in
  the docs. The rule advises, and only about the past and about people.
- **A separate judge per style.** Rejected: two judges disagree within a month, as the Go
  and Buzz selling-word lists already do.
- **Named profiles and a severity of the judge's own.** Rejected on review: they spelled
  again what the guard's decisions and a workspace's override table already say, and a
  second vocabulary for one idea reads as bolted on.
- **An `MGS` code per writing rule.** Rejected: `MGS` codes name magus's own failures and
  each carries a hand-written resolution page; a writing rule is a convention, so it
  takes a guard-style name and a code in a prefix of its own.
- **Vale.** Rejected earlier (see the vale-port plan): a spell for prose rules put policy
  in a tool magus ships to users; the Go package is the seam.

## Consequences

- Every rule this ADR adds is measured against this tree before it is turned on; an
  error-tier rule with a false positive here is not an error.
- Denials name the target shape and one example, so an agent that only reads the
  denial writes the new shape.
- Descriptions grow by a sentence or two in the lead; the budget past the lead is
  unchanged.
- `docs/conventions.md` stops claiming every rule is an error; it names the two tiers.
- The tone rules state posture, not warmth. A person still reads text written in their
  name before it is pushed; the judge does not certify it.
