---
title: "ADR 0037: proofread judges a text against its author"
order: 37
description: Proofread's rules only take things away, so a text that clears all of them comes out evenly shaped, and that evenness is itself what machine-written text looks like. Proofread stops pushing every text toward one flawless shape. The counting rules take their budgets from the author's voice file, a new rule flags text more even than its author, voice eval gains a paired mode that picks the features, and fixes leave the author's own specifics alone.
tags: [adr, decision, proofread, voice]
status: accepted
date: 2026-11-09
---

# ADR 0037: proofread judges a text against its author

## Context

Every proofread rule takes something away. A text that clears all of them comes out
evenly shaped: every item about the same length, every item opening on the same part of
speech, no series the rules dislike, no odd detail. That evenness is now itself a mark of
machine-written text. The fixes also go first for the details that show a person wrote
the text, such as a proper noun, an unusual specific or a short anecdote.

The pieces to fix this exist already:

- `voice-drift` (PRF1040) ranks a draft against 200-word windows of the author's own
  text and cuts at 5% of the author's windows (`libs/proofread/voice_drift.go`). It
  ranks each feature only in the direction agent drafts move.
- `Shape` measures `SentenceWordsStdDev` and `Triads`, but has no spread measure for
  paragraphs or items and none for openers (`libs/proofread/shape.go`).
- A voice moves one rule today: `tense` on a change description, through two fixed
  floors (`tenseFirstPersonFloor`, `tenseFutureFloor` in `author_voice.go`).
- `voice eval --owner --other` scores each `Shape` feature by AUC between two sets of
  texts, unpaired (`libs/proofread/cmd/proofread/voice.go`,
  `libs/proofread/internal/calibrate/separation.go`).

The voice-calibration study (2026-10-13) set voice-drift's features. Pooled into windows,
the features flagged 96-100% of agent drafts at 0-6% held-out owner false alarms, but
caught only 11-32% of light agent edits of the author's own text. An edit keeps the
topic and most of the words, so the features that find drafts miss it. Those edits
are the case this record is about.

## Decision

proofread adds no imperfection: a flaw added on purpose is a pattern too, and
detectable. It stops removing the author's own, and flags text smoother than its
author. `triad`'s page already says this for one case: one natural series is ordinary
writing.

### 1. Spread measures and the voice schema

`Shape` gains spread measures beside its means: the standard deviation of paragraph and
item words, the share of sentences and of list items whose first word is the most common
one, the entropy of the openers' parts of speech, and the share of sentences with a
subordinate clause. `VoiceStats` gains per-feature percentiles of the author's windows,
so a budget is a lookup and not a refit. The voice file's schema becomes
`proofread-voice/4`, and a `/3` file is refused with the command that rebuilds it, as
`/1` and `/2` are.

### 2. Paired eval picks the features

`voice eval --paired` reads the author's texts and a rewrite of each, joined by an `id`
field, and scores how often a feature moves the same way across the pairs. Pairs hold
topic fixed, so a feature that separates them separates style. A feature is kept for a
budget or for `uniform` only if, at 5% false alarms on the author's own windows, the
same cut voice-drift holds to, it still flags rewrites. A feature that fails is dropped,
and the record below says which.

### 3. Budgets from the voice

A rule that counts a habit declares the voice feature it counts and a default budget. With
a voice file, the budget is the author's own 95th percentile of that feature over their
windows. Without one, or when the author's 95th percentile is under the default, the
default stands, so a voice only ever relaxes a rule. `triad` comes first.

### 4. A uniformity rule

`uniform` flags a text more even than its author: it fires when the text's spread falls
below the author's 5th percentile on two or more of the spread measures stage 2 keeps.
It is voice-drift pointed the other way. `list-parallel` checks grammatical shape (each
item opens with a verb in one tense) and keeps rewarding it. `repeated-opener` and
`uniform` check the words and lengths. A list of past-tense verbs that are different
verbs passes both.

### 5. Protected spans

`-o json` gains `protected` spans, and the text output notes them: a rare proper noun,
a number with a unit, and a clause led by "until", "when" or "after" with a concrete
subject. A finding whose span covers a protected one is advise at most. It is an
annotation, not a rule.

### 6. Short texts

voice-drift abstains under 200 words, which skips commit messages, bullets and short
replies. `uniform` and the budgets judge the items of one list, or the texts passed
together on stdin, as one set.

| Item                                                                                       | State   |
| ------------------------------------------------------------------------------------------ | ------- |
| Spread measures in `Shape`; per-feature percentiles in `VoiceStats`; `proofread-voice/4`   | planned |
| `voice eval --paired`, and the calibration re-run on paired text                           | planned |
| Voice budgets for the counting rules, `triad` first                                        | planned |
| `uniform`; `list-parallel` checks grammatical shape and `repeated-opener` checks the words | planned |
| Protected spans in `-o json` and the text output; a fix that covers one is advise at most  | planned |
| Short texts judged as a set: the items of one list, or the texts passed together on stdin  | planned |

### Principles checked

- [Measured, not asserted](../design/principles/measured-not-asserted.md): a feature is
  kept only with a paired measurement at a stated false-alarm rate, and a feature the
  measurement drops is recorded here.
- [A measure is not the goal](../design/principles/a-measure-is-not-the-goal.md): this
  record exists because clearing every rule became the goal and produced its own tell.
  A budget from the author holds a text to the author, not to the rules' floor.
- [Verdicts are provenance-blind](../design/principles/verdicts-are-provenance-blind.md):
  `uniform` compares a text with its author's own measures, never with a guess at who or
  what wrote it. The same text by the same author gets the same finding whatever drafted it.
- [No silent defaults](../design/judgment/no-silent-defaults.md): a budget names its
  source, the author's percentile or the default, in the finding.
- [Delete before you add](../design/principles/delete-before-you-add.md): the tense floors
  become one case of the budget lookup, and no second voice mechanism is added.

## Alternatives

- Add imperfection: vary lengths, keep a typo, drop a comma. It lost because a flaw added
  on purpose has its own distribution, and a detector learns it as it learned the polish;
  it also puts errors in text a person signs. Reopen only with a measurement showing
  added variation that a detector trained on it cannot find.
- Raise every default budget instead of reading the voice. It lost because one budget fits
  no two authors: the study measured the first person at 20 to 21 per 1000 words in one
  author's descriptions against 0.2 and 1.9 in agent text, so a shared floor is wrong for
  one side. Reopen if voice files prove too costly to build.
- Fold uniformity into voice-drift as more features. It lost because voice-drift ranks
  each feature in one direction, toward agent drafts, and adding the other direction to
  its Fisher sum would change its tested false-alarm rate. A separate rule keeps both
  measured. Reopen if paired eval shows the two fire on the same texts.
- Make protected spans a rule that denies deleting them. It lost because a rule judges one
  text, and a deletion is a difference between two. Reopen when proofread judges a
  diff.

## Consequences

- With a voice file, a text is judged against its author and two authors get different
  budgets for the same rule. Without one, nothing changes.
- A `proofread-voice/3` file is refused until it is rebuilt.
- The calibration re-run's numbers decide which spread measures ship, so some named in
  part 1 may be measured and never read by a rule.

## Open questions

- Whether `uniform` ships at advise or off. Rules with no labelled firings ship off by
  convention.
- Which paired data counts: the light edits the 2026-10-13 study kept, rewrites of the
  author's older public text under the current rules, or older commit messages too.
- Whether a declared kind can name the voice kind it is held to. A voice measures four
  built-in kinds today, and declared kinds have no way into that list.
