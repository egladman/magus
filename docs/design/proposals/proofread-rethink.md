---
title: Rethinking proofread
description: Adopted in direction 2026-11-24, not built. proofread keeps its findings contract, decisions, labeling rounds and voice statistics, and replaces its regex middle with one document model, tagged pattern rules, and kind, audience and voice as separate axes. Voice grows into a per-author model that also writes a style brief.
tags: [proofread, voice, rules, calibration, privacy, proposal]
---

# Rethinking proofread

State: the direction and the first slice adopted 2026-11-24; nothing built. The
questions Eli answered are under [Adopted](#adopted); the rest are under
[Still open](#still-open).

proofread is meant to be the writing tool for every kind of text its author writes:
pull request descriptions, READMEs, commit messages, code comments, design docs and
messages to engineers and to people outside engineering. The goal is to write faster
while the text keeps its author's voice and intent. This page is the design for
getting there from [libs/proofread](../../../libs/proofread/README.md) as it stands.

The verdict: no rewrite from nothing. The findings contract, the decisions table,
kinds as values, the frozen labeling rounds and the voice statistics are sound and
rare in this field. The middle is what holds proofread back: about 100 rules that
each re-read the text with their own regexes and exemption switches, with no shared
tokens, and a voice model that only detects drift in four kinds over 200 words. The
labeled cases are the asset a rewrite would throw away, so the port goes rule family
by rule family against them.

## What stays

- The findings JSON, SARIF and rdjson output, decisions (`off`, `advise`, `deny`),
  grade, suppressions, baselines and protected spans.
- A kind as a value, and user kinds that cannot loosen a repository's table.
- The round discipline. `evidence.go` already records which rules are weak:
  `absolute` 6 of 40, `buzzword-weak` 10 of 40, `verdict` 16 of 40, `buzzword` 26 of 40.
  No other tool surveyed measures its own rules this way.
- voice-drift's statistics. A rank among the author's windows plus the draft is a
  conformal p-value, and the cut is the author's own 5%, so the false-alarm rate holds
  although Fisher's method assumes independent features
  ([ADR 0037](../../decisions/0037-proofread-judges-a-text-against-its-author.md)).
- Speed and no runtime dependencies. `proofread reference` reads a 3,972-word page in
  0.01 s wall, process start included (three runs, one machine).

## What is replaced

- No shared analysis. Each judge splits paragraphs and sentences itself, and `input`
  grows one optional pointer per format.
- Word senses told apart by neighbouring regexes. `intentExempt` in `tone.go` uses five
  regexes and a `switch` to tell "the cause lies in" from "the tool lies"; the tagger
  that could answer feeds only `Shape`.
- Global, hand-kept word lists. The lowest measured precisions are all word-list rules.
- Audience as eleven booleans on `KindSpec` (`Review`, `ToReviewers`, `Terse`, ...),
  fused with kind, so "a design doc for a stakeholder" cannot be said.
- A voice of nine hand-picked features, each fixed in one direction, that abstains
  under 200 words (most commits, comments and messages) and only detects.
- Few findings carry a fix a machine can apply.
- `kinds.json`, a file a person edits, in JSON.

## The design

Five layers, each built once and read by the ones above.

### Document

One parse per text into an immutable `Document`. Blocks (heading, paragraph, list
item, quote, code, table, title, and a comment with its place: trailing, body, doc or
file header) hold sentences and tokens. A token carries its text, a folded form, its
span, line and column, and flags for code span, quoted, platform text and protected.
Every reader (Markdown, message, lines, doc comment, code, stylesheet, template)
produces this one type. Annotations are lazy and computed once: the existing tagger's
tags, chunks from tags alone (noun phrase, verb group, clause, subject, passive),
`Shape` measures over the same tokens, and for the deep tier a parse and embeddings.

### Rules

A rule declares its id, PRF code, dimension, the annotations it needs, its scope and
its evidence record, in one of three forms, cheapest first:

- A pattern rule is data: a short sequence of token predicates (word set, lemma, tag,
  shape, flags), a skip, a scope, `unless` context predicates (in a quote, first-person
  subject, under a limits heading, next token a preposition) and a fix template. All
  pattern rules compile into one automaton over folded words, so the text is scanned
  once. `intent` becomes about ten lines of data.
- A measure rule is data: a measure, a scope, a comparator and a budget source
  (default, kind, audience or the author's percentile). One evaluator serves comment
  budgets by place, sentence length, triads and `uniform`.
- A Go rule holds what needs code: `lead-context`, `credit`, `list-parallel`,
  `restates-diff`.

The pattern vocabulary is closed: no variables, loops or arithmetic. What it cannot
say is a Go rule. Built-in patterns are generated to a Go table at build time, so
startup stays flat.

### Kind, audience and voice

Three axes in place of `KindSpec`'s booleans. Kind is what the text is: its reader and
structure. Audience is who reads it, and sets decisions, budgets and word packs: a
reader outside engineering needs jargon glossed, a stakeholder gets the outcome and a
number first, a reviewer gets evidence for claims. Voice is whose it is.

Resolution runs built-in, kind, audience, the repository's table, the user profile
(which only adds), then flags. Every finding names the layer that set its decision or
budget.

### Voice as a model

One model per author, with backoff from kind to register to all of the author's text,
weighted by word count, so a kind with little text borrows from the rest and the
finding says so.

- Profile: quantiles of every `Shape` measure, a function-word z-profile, tag 3-gram
  and punctuation rates, sentence-length and opener distributions.
- Contrast: from owner and rewrite pairs, a sparse linear model learns which features
  move and which way. A feature enters only with held-out separation at the 5% owner
  false-alarm cut, as ADR 0037 requires.
- Personal word lists: weighted log-odds between the author's texts and their rewrites.
  Words rewrites add that the author never writes become that author's tells; words
  the author favours are protected from `wordy` and `filler`.
- Scoring: a conformal p-value against held-out windows. Short texts pool by branch,
  thread or `--set`; under the floor the rule says it abstained.
- `proofread voice brief --kind K --audience A` prints the author's measured habits,
  preferred and avoided words and a few exemplar excerpts. Whatever drafts the text
  reads it first, and proofread judges the draft. proofread calls no model.

Training is honest: build on older text and test on newer; store the held-out
false-alarm rate, the paired detection rate, the pair count and the date in the voice
file; take rewrites from two or more models; and admit only text the author wrote,
since agent-assisted text in the reference teaches the model what it exists to catch.

### Findings and tiers

A finding gains `evidence` (value, budget, source layer), `fixes` (span, replacement,
safe or suggested) and `precision` (the rule's measured weight). The fast tier serves
hooks with tokens, tags, chunks, measures and a voice lookup. The deep tier serves
lint and CI with a parse, embeddings, cross-document rules and voice evaluation,
cached by content hash. A rule's tier follows from what it needs, and a hook asked
for a deep rule refuses and names it. Calibration keeps its method; a pattern rule's
freeze hash is the hash of its data, and precision is recorded per kind and audience
where a round has the firings.

## The privacy boundary

Everything a person adds lives at user level, in YAML:

- `$XDG_CONFIG_HOME/proofread/profile.yaml`: kinds, audiences, word packs, pattern rules,
  and bindings from path globs to a kind and audience.
- `$XDG_CONFIG_HOME/proofread/voice/`: voice files.
- `$XDG_DATA_HOME/proofread/`: exemplar texts and the user's own labeled cases.
- `$XDG_STATE_HOME/proofread/`: outcomes, as today.

`kinds.json` is replaced, with no reader for the old file.

Every kind, audience, rule and word pack carries an origin: built-in, repository or
user. A user-origin name never reaches a repository:

- A repository table cannot name one, and an unknown kind there is an error that
  lists no user names.
- Output written into a work tree (`--baseline`, SARIF, rdjson, `--record` exports)
  refuses when a user-origin kind was judged, as `voice build` refuses its default
  path inside a git work tree.
- On a pipe a user-origin kind prints as `user` and a user rule as
  `user/<short hash>`; the real name prints only on a terminal.
- A user kind is selected by a binding in the profile or by `--profile-kind`, so its
  name stays out of `argv`, shell history, the guard's journal and hook logs.
- A user rule is suppressed by its hash form, and the voice file keys user kinds by
  the same hash, so no committed file carries the name.
- A user profile adds and never loosens: inside a repository the repository's
  decisions hold.
- A lint in each repository that uses proofread can refuse a committed file that names
  a user-origin kind, reading the names from that machine's profile only.

## Slices

The port happens inside the module. `JudgeText`, the findings JSON, PRF codes, the
decisions table and the CLI stay as they are until the last slice.

- Slice 0, no behavior change: record every rule's firing set over the labeled cases
  and round samples, and `BenchmarkJudgeText` per kind.
- Slice 1: the `Document`, shared tokens and tags, the pattern engine, and the six
  worst-measured rules ported (`intent`, `blame`, `verdict`, `absolute`, `buzzword`,
  `buzzword-weak`, 0.07 to 0.50 precision). `Shape` moves onto the same tokens. Done
  means every changed firing on the labeled cases is listed and labeled, a fresh
  frozen round scores the six rules, and the benchmark is no slower.
- Slice 2: voice: profile quantiles, backoff, learned contrast, personal word lists,
  pooled short texts and `voice brief`. A new voice schema; old files are refused with
  the rebuild command.
- Slice 3: `profile.yaml`, audiences, origins and the redaction refusals.
- Slice 4: the remaining families, measure rules, the stylesheet and code readers onto
  `Document`; the regex judges and `prefilter.go` are deleted.
- Slice 5: the deep tier (a chunker first, a parser only on a measured need),
  embedding-backed restatement rules, and `proofread fix --safe`.

The `code-comment` kind goes ahead on its own and is absorbed in slice 4: its scanner
becomes a `Document` reader, its budgets by place become measure rules, its wording
rules become pattern rules, and the `watchdog.go` fixture becomes the port's
equivalence test. Its judges keep place attribution out of themselves so the move is
mechanical.

## Adopted

Ruled 2026-11-24:

- The direction above, and slice 1 first, after the slice 0 oracle. The order of the
  later slices is free, as long as all of them merge.
- One voice per person; no team voice.
- The hook budget: 20 ms cold at p95 for 1,000 words.
- The voice file may hold the author's preferred and avoided style words. It holds no
  names, identifiers or paths, and never leaves the author's machine.
- Voice trains mainly on text the author dictated, which is plentiful. Dictation
  errors are tolerated; pasted and injected text is excluded.
- A user profile may declare pattern rules. This reopens
  [ADR 0033](../../decisions/0033-a-kind-is-a-reader-and-its-rules.md) at user level
  only.
- Audiences are named, not sets of properties: `reviewer`, `engineer`, `stakeholder`,
  `newcomer`, `agent` and `public`.
- The deep tier may read an embedding table, for lint only: magus's existing table,
  for the restatement rules only, which advise until a round measures them.
- proofread applies safe fixes itself, under `proofread fix --safe`.
- proofread stays in `libs/proofread`.

## Still open

- Whether exemplar texts for `voice brief` are kept on disk, and from what date the
  author's text counts as reference.
- The default decision of a user-level pattern rule.
- The amendments the slices owe: ADR 0033 for user-level pattern rules and ADR 0037
  for learned weights and style words, each changed in the commit that builds it.

## Departures from the design principles

- [ADR 0033](../../decisions/0033-a-kind-is-a-reader-and-its-rules.md) gives a declared
  kind no pattern language. Pattern rules are a small one, because word-in-context
  rules are one algorithm with different data, and that data now hides in regexes
  where it cannot be hashed for a freeze, diffed or extended privately. Bound: the
  closed vocabulary.
- "Calibration, not training" in the proofread README. Voice learns weights. Bound:
  linear, per author, trained on the author's machine, with held-out numbers stored
  beside the weights.
- "The voice file holds no content word." Personal word lists hold style words. Bound:
  user level only, style words only.
- JSON Lines for machine state. The voice file stays one JSON document: a built model
  replaced whole, not an append-only log.
- A core that knows no natural language. proofread is English-only, says so, and
  refuses other text.

Followed, not departed: verdicts stay provenance-blind, so no detector asks who wrote
a text; backoff and budgets name their source
([no silent defaults](../judgment/no-silent-defaults.md)); a file a person edits is
YAML; `prefilter.go`, the exemption functions and the eleven booleans are deleted
before anything is added; and slice 1 passes only on a fresh round.

## Research behind it

The fit test was a Go library answering inside an edit hook in milliseconds, offline.

| Approach                                                           | Verdict                                                                                                           |
| ------------------------------------------------------------------ | ----------------------------------------------------------------------------------------------------------------- |
| Word-list and regex linters (write-good, proselint, Vale)          | Keep only as the anchor matcher under tagged patterns; proofread's rounds measure them at 0.14 to 0.65 precision  |
| Token patterns over tags (Vale `sequence`, LanguageTool, Harper)   | The right rule form; built in Go on the existing tagger, importing none of them                                   |
| Averaged-perceptron tagging                                        | Already embedded (UPOS floor 0.93 on EWT); promoted from `Shape` to every rule                                    |
| Dependency parsing (spaCy, UDPipe)                                 | Deep tier only, after a chunker over tags proves insufficient; no maintained pure-Go parser was found              |
| Perplexity detectors (DetectGPT, Binoculars)                       | Rejected: GB of weights, fragile under RAID, and they ask who wrote a text, which ADR 0037 refuses                |
| Function-word stylometry (Burrows' and Cosine Delta)               | The core of the voice model; pooling short texts into windows answers the length floor                            |
| Tag and character n-gram profiles                                  | Added to the voice profile, tag n-grams first since they carry no topic                                           |
| Contrast vocabulary (Kobak excess words, Monroe weighted log-odds) | Learns each author's tells from pairs in place of global lists                                                     |
| Register grammar rates (Biber, Reinhart et al.)                    | Already in `Shape`; kept                                                                                           |
| Style and static embeddings (LUAR, model2vec)                      | Not for voice; a static table serves restatement in the deep tier                                                  |
| Neural detectors, readability formulas                             | Rejected                                                                                                           |
| Conformal calibration                                              | Names what voice-drift already does, extended to every voice score                                                 |
| Few-shot style imitation (Wang et al., EMNLP 2025)                 | proofread hands a drafting model exemplars and numbers, then judges the result                                    |

The measured, explainable and cheap techniques are all classical, and proofread
already has the hardest part of each. Nothing argues for a neural model in the hook.

Sources: Hans et al., [Binoculars](https://arxiv.org/abs/2401.12070); Dugan et al.,
[RAID](https://aclanthology.org/2024.acl-long.674); Liang et al.,
[detectors and non-native writers](https://arxiv.org/abs/2304.02819);
[conformal bounds for detection](https://arxiv.org/pdf/2505.05084); Eder,
[Does size matter?](https://www.doi.org/10.1093/LLC/FQT066); Evert et al.,
[Delta measures](https://www.doi.org/10.1093/LLC/FQX023); Monroe, Colaresi and Quinn,
[Fightin' Words](https://languagelog.ldc.upenn.edu/myl/Monroe.pdf); Kobak et al.,
[excess vocabulary](https://arxiv.org/abs/2406.07016v5); Rivera-Soto et al.,
[LUAR](https://aclanthology.org/2021.emnlp-main.70); Wang et al.,
[Catch Me If You Can? Not Yet](https://arxiv.org/abs/2509.14543v1); Honnibal,
[a POS tagger in 200 lines](https://explosion.ai/blog/part-of-speech-pos-tagger-in-python);
[spaCy speed and accuracy](https://spacy.io/usage/facts-figures);
[UDPipe 2](https://ufal.mff.cuni.cz/node/2110);
[Vale `sequence`](https://vale.sh/docs/checks/sequence);
[Harper](https://github.com/automattic/harper);
[model2vec](https://github.com/MinishLab/model2vec);
[TAACO](https://link.springer.com/10.3758/s13428-015-0651-7).
