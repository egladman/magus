# proofread: adding rules and calibrating them

This page is for changing proofread itself: adding a rule or a kind of text, and
deciding what a rule ships as. Using proofread is in
[`libs/conventions/README.md`](../README.md#proofread) and the
[proofread guide](../../../docs/guides/proofread.md).

## Calibration, not training

No model is trained here. Every rule is hand-written Go that a reader can audit, and
nothing at check time learns or loads weights. What the labeled data does is
calibrate: it measures how often a hand-written rule is right on text nobody tuned it
on, and that measurement decides the rule's default decision (`off`, `advise` or
`deny`). Fixing a rule is ordinary code review. Promoting one takes evidence from a
frozen labeling round.

## Where things live

| Path                          | Holds                                                                                                   |
| ----------------------------- | ------------------------------------------------------------------------------------------------------- |
| `proofread.go`                | the `Rule`, `Kind` and `Decision` types, the check table and the core rules                             |
| one file per family           | each family's rule constants, checks, texts and judges: `tone.go`, `slop.go`, `review.go`, `density.go` |
| `catalog.go`                  | the PRF code families, the dimensions and the merge of every family's texts                             |
| `text.go`                     | the readers: Markdown and plain text into prose lines, and the platform text blanked before any rule    |
| `evidence.go`                 | `evidenceCeiling`: the strictest decision each rule's labeled evidence supports                         |
| `calibrate.go`, `round.go`    | the case loader, the precision tally, Wilson and Krippendorff                                           |
| `testdata/cases/<rule>.txtar` | the labeled cases: regression tests and the tuning set                                                  |
| `testdata/guideline.md`       | the labeling guideline every round is frozen with                                                       |
| `testdata/rounds/roundN.json` | one record per labeling round: counts, scores and gates, no text                                        |

## Add a rule

1. Pick its family file, or start one beside `help.go` if no family fits. Declare the
   `Rule` constant there, its `check` (the kinds it judges, any kind it only advises
   on, and `house` if it encodes one repository's conventions), and its `ruleText`:
   the next free PRF code in its family (`catalog.go` lists them), a dimension, a
   one-line `catches` and a `why` that says what the rule protects and what measured
   it.
2. Write the judge. Read the prose the reader hands you; never re-parse Markdown.
   Leave quoted words, code and platform text alone: they are not the author's.
3. Add table tests beside the judge, both directions: what fires and what must not.
4. Add `testdata/cases/<rule>.txtar` with at least one `hit/` and one `pass/` section
   (`TestLabeledCasesAgreeWithTheRules` requires both). Write the texts yourself.
5. Add the rule to `evidenceCeiling` at `DecisionOff`, unless it is a house rule,
   which ships off anyway. No rule ships on before a frozen round measures it.
6. Run `magus run go-test libs/conventions`, then `magus affected generate:rw` for the
   rule's reference page, and add a changelog fragment.

## Add a kind

1. Declare the `Kind` in its family file, or in `proofread.go` beside the others.
2. Route it in `judgeText` (`text.go`) to a reader, an existing one if the text is
   Markdown or plain text, a new one if it is not, as `judgeStylesheet` reads CSS.
3. Decide rule by rule which rules judge it, by adding the kind to each check's kind
   list. A rule written for running prose usually does not suit fragments.
4. Add it to `everyKind` (`suppress.go`) and, if the text is read from files, to the
   CLI's subcommand table in `cmd/proofread/main.go`.
5. Add its row to the kind table in `libs/conventions/README.md`.

## Earn a default

A rule's default moves only on a frozen labeling round, run with
`proofread calibrate round`; [the round guide](../../../docs/guides/proofread-calibration.md)
walks through it step by step. In short:

| Step       | What happens                                                                                                |
| ---------- | ----------------------------------------------------------------------------------------------------------- |
| sample     | seeded, deterministic firings from text no earlier round or committed case holds, at least 10 per source    |
| freeze     | the rule commit, seed, gates, guideline digest, binary sha256 and every source's sha256 are recorded first  |
| label      | two labelers, alone, a note on every label                                                                  |
| adjudicate | someone who never tuned the rule settles each disagreement                                                  |
| score      | precision, Wilson lower bound, alpha and the per-source gate; a rule file changed since the freeze voids it |
| pool       | a deny weighs two frozen rounds on different sources                                                        |

The gates: advise needs a Wilson 95% lower bound of 0.70 over 30 or more firings, on
the worst source with 10 or more, with at most one disagreement per 10 firings (or
alpha 0.80) and a note on every one. A rule's repeat attempt is frozen with z = 2.24,
so trying again cannot pass by luck. Deny needs two rounds on different sources,
pooled to 60 firings with at most 1 wrong, both passing agreement. A rule that passes
gets 10 of its agreed-correct labels spot-checked by a person before its
`evidenceCeiling` entry moves.

## Tune without fooling yourself

- The committed cases are the tuning set. A round's texts are spent the moment
  anyone tunes on them, so a fix read off a round is measured by the next one.
- Never read the held-out fifth of the cases (`Case.HeldOut`: the fnv64a hash of the
  text, modulo 5). It is a smoke test, not evidence.
- Sealed sets live outside git, under
  `$XDG_CACHE_HOME/proofread/calibration/sealed/`, and the guard refuses a shell line
  that reads them.
- A case a fix stops firing on moves from `fp/` to `pass/` with
  `was=fp:<round>:<id>` on its section header; a lint refuses the move without it.
- Never delete or relabel a labeled case to make a number move.

## History

Rounds 1 to 3 calibrated the first ceilings; rounds 4 to 7 tested fix rounds. Each
round's record is in `testdata/rounds/`, and `evidenceCeiling` cites the round behind
every cap.
